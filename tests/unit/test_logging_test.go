package tests

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"ssh_proxy/src/proxy"
	"ssh_proxy/tests/helpers"
)

func TestRunLogRetention(t *testing.T) {
	for _, tc := range []struct {
		name, config string
		limit        int
	}{
		{"one", "[logging]\nmax_log_files = 1", 1},
		{"two", "[logging]\nmax_log_files = 2", 2},
		{"default", "", 10},
		{"invalid-config-still-logs", "logging = [", 10},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			p := proxy.Paths{Config: filepath.Join(dir, "config.toml"), Logs: filepath.Join(dir, "logs"), ID: "abcdef0123456789"}
			if err := os.WriteFile(p.Config, []byte(tc.config), 0600); err != nil {
				t.Fatal(err)
			}
			proxyLogs := testutil.SeedRunLogs(t, p.Logs, "logs", 11)
			trayLogs := testutil.SeedRunLogs(t, p.Logs, "tray", 11)
			unrelated := filepath.Join(p.Logs, "logs_notes.txt")
			if err := os.WriteFile(unrelated, []byte("keep me"), 0600); err != nil {
				t.Fatal(err)
			}
			for index, kind := range []string{"logs", "tray"} {
				logger, file, err := proxy.NewRunLog(p, kind, false)
				if err != nil {
					t.Fatal(err)
				}
				logger.Print("current run stays writable")
				file.Close()
				old := proxyLogs
				if kind == "tray" {
					old = trayLogs
				}
				for i, path := range old {
					_, err := os.Stat(path)
					if (err == nil) != (i >= len(old)-tc.limit+1) {
						t.Fatalf("wrong retained age: %s: %v", path, err)
					}
				}
				if index == 0 {
					for _, path := range trayLogs {
						if _, err := os.Stat(path); err != nil {
							t.Fatal("proxy removed tray log", path, err)
						}
					}
				}
				data, err := os.ReadFile(file.Name())
				if err != nil || !strings.Contains(string(data), "current run stays writable") {
					t.Fatal("current log removed", err)
				}
			}
			files, err := os.ReadDir(p.Logs)
			if err != nil || len(files) != 2*tc.limit+1 {
				t.Fatalf("expected independent limits plus unrelated file: %d: %v", len(files), err)
			}
			if data, err := os.ReadFile(unrelated); err != nil || string(data) != "keep me" {
				t.Fatal("unrelated file changed", err)
			}
		})
	}
}

func TestLogRetentionConfig(t *testing.T) {
	for _, limit := range []int{-1, 0, 1, 10} {
		t.Run(fmt.Sprint(limit), func(t *testing.T) {
			path, _ := testutil.Config(t, "Host test\n HostName example.invalid\n User tester\n")
			text := strings.Replace(string(testutil.ConfigTemplate(t)), "max_log_files = 10", fmt.Sprintf("max_log_files = %d", limit), 1)
			if err := os.WriteFile(path, []byte(text), 0600); err != nil {
				t.Fatal(err)
			}
			cfg, err := proxy.LoadConfig(path)
			if limit < 1 {
				if err == nil || !strings.Contains(err.Error(), "logging.max_log_files") {
					t.Fatal("invalid limit accepted", err)
				}
			} else if err != nil || cfg.Logging.MaxLogFiles != limit {
				t.Fatal("limit not loaded", err)
			}
		})
	}
}
