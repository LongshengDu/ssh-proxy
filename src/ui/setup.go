//go:build windows || darwin

package ui

import (
	"os"
	"path/filepath"

	"ssh-proxy/src/config"
)

// SetupPrompts remembers UI actions only; all proxy state comes from the CLI.
type SetupPrompts map[string]bool

func (seen SetupPrompts) Config(result config.ConfigCheck) string {
	key := "config:" + result.EditFile
	if result.Valid || result.EditFile == "" || seen[key] {
		return ""
	}
	seen[key] = true
	return result.EditFile
}

func openEditable(path string) error {
	if _, err := os.Stat(path); os.IsNotExist(err) {
		if err = os.MkdirAll(filepath.Dir(path), 0700); err != nil {
			return err
		}
		file, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
		if err != nil && !os.IsExist(err) {
			return err
		}
		if file != nil {
			file.Close()
		}
	}
	return openPath(path)
}
