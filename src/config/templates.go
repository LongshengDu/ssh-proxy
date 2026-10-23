package config

import (
	"bytes"
	"errors"
	"fmt"
	"io/fs"
	"maps"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"time"

	"github.com/BurntSushi/toml"
)

// EnsureConfig prepares the user's files beside config.toml from the shipped
// support/*.in templates, silently. Packages ship only the templates, so
// replacing an installation never overwrites the user's files:
//   - each support/NAME.in is copied to NAME when NAME is missing;
//   - an existing config.toml gains settings support/config.toml.in added and
//     keeps every entry it has, including ones the program no longer uses. It
//     is rewritten only then, and its comments are not kept. Invalid TOML is
//     left for validation to report.
//
// Every installation ships support/; a missing template is an error.
func EnsureConfig(configPath string) error {
	dir := filepath.Dir(configPath)
	templates, err := filepath.Glob(filepath.Join(dir, "support", "*.in"))
	if err != nil {
		return err
	}
	for _, template := range templates {
		if err := CopyIfMissing(template, filepath.Join(dir, strings.TrimSuffix(filepath.Base(template), ".in"))); err != nil {
			return err
		}
	}
	return upgradeConfig(configPath, filepath.Join(dir, "support", "config.toml.in"))
}

// CopyIfMissing creates path from template unless path exists.
func CopyIfMissing(template, path string) error {
	if _, err := os.Stat(path); !errors.Is(err, fs.ErrNotExist) {
		return nil // Present, or not checkable: never replace a user's file.
	}
	data, err := os.ReadFile(template)
	if err != nil {
		return templateError(template, err)
	}
	file, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if errors.Is(err, fs.ErrExist) {
		return nil // Another process created it first.
	}
	if err != nil {
		return err
	}
	_, err = file.Write(data)
	return errors.Join(err, file.Close())
}

func upgradeConfig(path, template string) error {
	var defaults, user map[string]any
	if _, err := toml.DecodeFile(template, &defaults); err != nil {
		return templateError(template, err)
	}
	if _, err := toml.DecodeFile(path, &user); err != nil {
		return nil // Missing or invalid: LoadConfig reports it.
	}
	merged := mergeSettings(defaults, user)
	if reflect.DeepEqual(merged, user) {
		return nil
	}
	var text bytes.Buffer
	encoder := toml.NewEncoder(&text)
	encoder.Indent = ""
	if err := encoder.Encode(merged); err != nil {
		return err
	}
	// A unique name lets the tray and CLI autostart upgrade at the same moment.
	temp, err := os.CreateTemp(filepath.Dir(path), filepath.Base(path)+".*.tmp")
	if err != nil {
		return err
	}
	_, err = temp.Write(text.Bytes())
	err = errors.Join(err, temp.Close())
	if err == nil {
		// Windows refuses to replace a file another process has open for a moment.
		err = os.Rename(temp.Name(), path)
		for delay := 20 * time.Millisecond; err != nil && delay <= 640*time.Millisecond; delay *= 2 {
			time.Sleep(delay)
			err = os.Rename(temp.Name(), path)
		}
	}
	if err != nil {
		os.Remove(temp.Name())
	}
	return err
}

func templateError(template string, err error) error {
	if errors.Is(err, fs.ErrNotExist) {
		return fmt.Errorf("%s is missing; extract the complete release ZIP into this folder again", template)
	}
	return err
}

// mergeSettings keeps every entry the user has, unused or even invalid ones
// (validation reports those), and adds the template's settings that are missing.
func mergeSettings(defaults, user map[string]any) map[string]any {
	merged := maps.Clone(user)
	if merged == nil {
		merged = map[string]any{}
	}
	for key, value := range defaults {
		own, present := user[key]
		table, isTable := value.(map[string]any)
		ownTable, ownIsTable := own.(map[string]any)
		if !present {
			merged[key] = value
		} else if isTable && ownIsTable {
			merged[key] = mergeSettings(table, ownTable)
		}
	}
	return merged
}
