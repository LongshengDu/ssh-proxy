package config

import (
	"bytes"
	"errors"
	"io/fs"
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
//     drops settings it no longer has. It is rewritten only then, and its
//     comments are not kept. Invalid TOML is left for validation to report.
//
// Without support/*.in files (development and test layouts) it does nothing.
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

// CopyIfMissing creates path from template unless path exists or template is missing.
func CopyIfMissing(template, path string) error {
	if _, err := os.Stat(path); !errors.Is(err, fs.ErrNotExist) {
		return nil // Present, or not checkable: never replace a user's file.
	}
	data, err := os.ReadFile(template)
	if errors.Is(err, fs.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
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
	if _, err := toml.DecodeFile(template, &defaults); errors.Is(err, fs.ErrNotExist) {
		return nil
	} else if err != nil {
		return err
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
	temp := path + ".tmp"
	if err := os.WriteFile(temp, text.Bytes(), 0600); err != nil {
		return err
	}
	// Windows refuses to replace a file another process has open for a moment.
	err := os.Rename(temp, path)
	for delay := 20 * time.Millisecond; err != nil && delay <= 640*time.Millisecond; delay *= 2 {
		time.Sleep(delay)
		err = os.Rename(temp, path)
	}
	if err != nil {
		os.Remove(temp)
	}
	return err
}

// mergeSettings keeps the user's value for every setting the template has, adds
// the template's value for missing ones, and drops settings the template lacks.
func mergeSettings(defaults, user map[string]any) map[string]any {
	merged := map[string]any{}
	for key, value := range defaults {
		if table, ok := value.(map[string]any); ok {
			own, _ := user[key].(map[string]any)
			merged[key] = mergeSettings(table, own)
		} else if own, ok := user[key]; ok {
			merged[key] = own
		} else {
			merged[key] = value
		}
	}
	return merged
}
