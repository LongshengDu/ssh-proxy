package tests

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"ssh-proxy/src/config"
	"ssh-proxy/tests/helpers"
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

// config.toml has no [app] table; the setting lives only in app.json. An old
// [app] table in config.toml is an unused entry: kept and ignored.
func TestConfigIgnoresAppTable(t *testing.T) {
	path, _ := testutil.Config(t, "Host test\n HostName example.invalid\n User tester\n")
	data, err := os.ReadFile(path)
	if err != nil || strings.Contains(string(data), "[app]") {
		t.Fatal("public config.toml still has an [app] table", err)
	}
	if err = os.WriteFile(path, append(data, "\n[app]\nstart_proxy_on_launch = true\n"...), 0600); err != nil {
		t.Fatal(err)
	}
	if result := config.CheckConfig(path); !result.Valid {
		t.Fatal("an unused [app] table must be ignored", result.Error)
	}
}
