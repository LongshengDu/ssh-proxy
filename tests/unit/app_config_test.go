package tests

import (
	"os"
	"path/filepath"
	"testing"

	"ssh-proxy/src/config"
)

// The tray keeps Start Proxy When App Launches in app.json beside config.toml
// and writes the whole file; a missing file means the default (off).
func TestAppSettingsFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "app.json")
	if s, err := config.LoadAppSettings(path); err != nil || s.StartProxyOnLaunch {
		t.Fatal("missing app.json must mean the defaults", s, err)
	}
	for _, enabled := range []bool{true, false} {
		if err := config.SaveAppSettings(path, config.AppSettings{StartProxyOnLaunch: enabled}); err != nil {
			t.Fatal(err)
		}
		if s, err := config.LoadAppSettings(path); err != nil || s.StartProxyOnLaunch != enabled {
			t.Fatal("setting not saved", enabled, s, err)
		}
	}
	if err := os.WriteFile(path, []byte("{"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := config.LoadAppSettings(path); err == nil {
		t.Fatal("damaged app.json accepted")
	}
}
