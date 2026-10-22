package tests

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"ssh_proxy/src/config"
	"ssh_proxy/tests/helpers"
)

// The tray toggles app.start_proxy_on_launch by rewriting only that line, so
// the user's bilingual comments and other settings survive.
func TestStartProxyOnLaunchEditKeepsTheRestOfTheFile(t *testing.T) {
	template := string(testutil.ConfigTemplate(t))
	for _, tc := range []struct {
		name, before, after string
		enabled             bool
	}{
		{"public default", template, strings.Replace(template, "start_proxy_on_launch = false", "start_proxy_on_launch = true", 1), true},
		{"inline comment", "[app]\nstart_proxy_on_launch = true # keep this\n", "[app]\nstart_proxy_on_launch = false # keep this\n", false},
		{"missing key", "# note\n[app]\n\n[logging]\nmax_log_files = 3\n", "# note\n[app]\nstart_proxy_on_launch = true\n\n[logging]\nmax_log_files = 3\n", true},
		{"missing table", "[logging]\nmax_log_files = 3", "[logging]\nmax_log_files = 3\n\n[app]\nstart_proxy_on_launch = true\n", true},
		{"key in another table", "[other]\nstart_proxy_on_launch = false\n", "[other]\nstart_proxy_on_launch = false\n\n[app]\nstart_proxy_on_launch = true\n", true},
		{"crlf", "# 注释\r\n[app]\r\nstart_proxy_on_launch = false\r\n[ssh]\r\n", "# 注释\r\n[app]\r\nstart_proxy_on_launch = true\r\n[ssh]\r\n", true},
		{"crlf missing table", "[ssh]\r\n", "[ssh]\r\n\r\n[app]\r\nstart_proxy_on_launch = true\r\n", true},
		{"empty file", "", "[app]\nstart_proxy_on_launch = true\n", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "config.toml")
			if err := os.WriteFile(path, []byte(tc.before), 0600); err != nil {
				t.Fatal(err)
			}
			if err := config.SetStartProxyOnLaunch(path, tc.enabled); err != nil {
				t.Fatal(err)
			}
			if got, _ := os.ReadFile(path); string(got) != tc.after {
				t.Fatalf("got %q, want %q", got, tc.after)
			}
			if enabled, err := config.StartProxyOnLaunch(path); err != nil || enabled != tc.enabled {
				t.Fatal("setting not read back", enabled, err)
			}
			if entries, _ := os.ReadDir(filepath.Dir(path)); len(entries) != 1 {
				t.Fatal("temporary file left behind", entries)
			}
		})
	}
}

func TestStartProxyOnLaunchEditRefusesUnsafeFiles(t *testing.T) {
	dir := t.TempDir()
	invalid := filepath.Join(dir, "invalid.toml")
	if err := os.WriteFile(invalid, []byte("proxy = [\n"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := config.SetStartProxyOnLaunch(invalid, true); err == nil {
		t.Fatal("unparsable TOML was rewritten")
	}
	if data, _ := os.ReadFile(invalid); string(data) != "proxy = [\n" {
		t.Fatal("refused edit changed the file", string(data))
	}
	readOnly := filepath.Join(dir, "read-only.toml")
	if err := os.WriteFile(readOnly, []byte("[app]\nstart_proxy_on_launch = false\n"), 0400); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.Chmod(readOnly, 0600) })
	if err := config.SetStartProxyOnLaunch(readOnly, true); err == nil || !strings.Contains(err.Error(), "read-only") {
		t.Fatal("read-only config was changed", err)
	}
	if enabled, err := config.StartProxyOnLaunch(readOnly); err != nil || enabled {
		t.Fatal("read-only config value changed", enabled, err)
	}
	if runtime.GOOS != "windows" {
		if info, err := os.Stat(invalid); err != nil || info.Mode().Perm() != 0600 {
			t.Fatal("permissions changed", info.Mode(), err)
		}
	}
}

// A reader holding config.toml open, such as the tray's status poll or an
// editor, must not make the toggle fail; Windows blocks the replace meanwhile.
func TestStartProxyOnLaunchEditWaitsForBriefReaders(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.toml")
	if err := os.WriteFile(path, []byte("[app]\nstart_proxy_on_launch = false\n"), 0600); err != nil {
		t.Fatal(err)
	}
	reader, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	released := make(chan struct{})
	go func() {
		time.Sleep(300 * time.Millisecond)
		reader.Close()
		close(released)
	}()
	if err := config.SetStartProxyOnLaunch(path, true); err != nil {
		t.Fatal(err)
	}
	<-released
	if enabled, err := config.StartProxyOnLaunch(path); err != nil || !enabled {
		t.Fatal("setting not written", enabled, err)
	}
}

func TestStartProxyOnLaunchIsAValidSetting(t *testing.T) {
	path, _ := testutil.Config(t, "Host test\n HostName example.invalid\n User tester\n")
	if err := config.SetStartProxyOnLaunch(path, true); err != nil {
		t.Fatal(err)
	}
	cfg, err := config.LoadConfig(path)
	if err != nil || !cfg.App.StartProxyOnLaunch {
		t.Fatal("app.start_proxy_on_launch must pass validation", err)
	}
	if result := config.CheckConfig(path); !result.Valid {
		t.Fatal("check rejected the tray setting", result.Error)
	}
}
