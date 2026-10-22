package config

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/BurntSushi/toml"
)

// StartProxyOnLaunch reads app.start_proxy_on_launch without validating the
// rest of the file, so the tray can show it while setup is incomplete.
func StartProxyOnLaunch(configPath string) (bool, error) {
	var c Config
	if _, err := toml.DecodeFile(configPath, &c); err != nil {
		return false, err
	}
	return c.App.StartProxyOnLaunch, nil
}

var (
	tableHeader = regexp.MustCompile(`^\s*\[`)
	appHeader   = regexp.MustCompile(`^\s*\[\s*app\s*\]\s*(#.*)?$`)
	launchLine  = regexp.MustCompile(`^(\s*start_proxy_on_launch\s*=\s*)(true|false)?(.*)$`)
)

// SetStartProxyOnLaunch changes only the app.start_proxy_on_launch line,
// adding it, or an [app] table, when missing. Comments, other settings and line
// endings stay as written; re-encoding the TOML would drop the bilingual
// comments. The file is replaced atomically and only if it still parses.
func SetStartProxyOnLaunch(configPath string, enabled bool) error {
	info, err := os.Stat(configPath)
	if err != nil {
		return err
	}
	if info.Mode().Perm()&0200 == 0 {
		return fmt.Errorf("%s is read-only", configPath)
	}
	data, err := os.ReadFile(configPath)
	if err != nil {
		return err
	}
	newline := "\n"
	if strings.Contains(string(data), "\r\n") {
		newline = "\r\n"
	}
	value := strconv.FormatBool(enabled)
	lines := strings.Split(string(data), "\n")
	app, done := -1, false
	for i, line := range lines {
		text, cr := strings.CutSuffix(line, "\r")
		if appHeader.MatchString(text) {
			app = i
			continue
		}
		if app < 0 {
			continue
		}
		if tableHeader.MatchString(text) {
			break
		}
		if match := launchLine.FindStringSubmatch(text); match != nil {
			rest := match[3]
			if match[2] == "" {
				rest = "" // Replace an unparsable value entirely.
			}
			lines[i] = match[1] + value + rest
			if cr {
				lines[i] += "\r"
			}
			done = true
			break
		}
	}
	setting := "start_proxy_on_launch = " + value
	text := strings.Join(lines, "\n")
	switch {
	case done:
	case app >= 0:
		lines = slices.Insert(lines, app+1, setting+strings.TrimSuffix(newline, "\n"))
		text = strings.Join(lines, "\n")
	default:
		if text != "" && !strings.HasSuffix(text, "\n") {
			text += newline
		}
		if text != "" {
			text += newline
		}
		text += "[app]" + newline + setting + newline
	}
	var check Config
	if _, err := toml.Decode(text, &check); err != nil || check.App.StartProxyOnLaunch != enabled {
		return fmt.Errorf("cannot set app.start_proxy_on_launch in %s: %v", configPath, err)
	}
	temp, err := os.CreateTemp(filepath.Dir(configPath), ".config-*.toml")
	if err != nil {
		return err
	}
	defer os.Remove(temp.Name())
	if _, err = temp.WriteString(text); err == nil {
		err = temp.Chmod(info.Mode().Perm())
	}
	if closeErr := temp.Close(); err == nil {
		err = closeErr
	}
	if err != nil {
		return err
	}
	return replaceFile(temp.Name(), configPath)
}

// replaceFile retries for about two seconds: on Windows any open handle, such
// as the tray's own status poll, an editor or a virus scanner, briefly blocks
// replacing the file.
func replaceFile(from, to string) error {
	err := os.Rename(from, to)
	for delay := 10 * time.Millisecond; err != nil && delay <= 1280*time.Millisecond; delay *= 2 {
		time.Sleep(delay)
		err = os.Rename(from, to)
	}
	return err
}
