package config

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
)

// AppSettings are the Windows/macOS tray's own preferences, kept in app.json
// beside config.toml. The tray writes the whole file; it has no comments to keep.
type AppSettings struct {
	StartProxyOnLaunch bool `json:"start_proxy_on_launch"`
}

// LoadAppSettings returns the defaults when app.json does not exist yet.
func LoadAppSettings(path string) (AppSettings, error) {
	var s AppSettings
	data, err := os.ReadFile(path)
	if errors.Is(err, fs.ErrNotExist) {
		return s, nil
	}
	if err == nil {
		err = json.Unmarshal(data, &s)
	}
	if err != nil {
		return s, fmt.Errorf("read %s: %w", path, err)
	}
	return s, nil
}

func SaveAppSettings(path string, s AppSettings) error {
	data, err := json.MarshalIndent(s, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, append(data, '\n'), 0644)
}
