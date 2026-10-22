package tests

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"ssh_proxy/src/config"
	"ssh_proxy/src/logs"
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
			p := config.Paths{Config: filepath.Join(dir, "config.toml"), Logs: filepath.Join(dir, "logs"), ID: "abcdef0123456789"}
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
				logger, file, err := logs.NewRunLog(p, kind, false)
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

func TestRunLogSizeLimit(t *testing.T) {
	path := filepath.Join(t.TempDir(), "logs", "run.txt")
	logger, writer, err := logs.NewLog(path, false)
	if err != nil {
		t.Fatal(err)
	}
	writer.SetLimit(1)
	// Child output and native ssh -E append through their own handles.
	other, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0600)
	if err != nil {
		t.Fatal(err)
	}
	payload := strings.Repeat("x", 1000)
	for i := 0; i < 3000; i++ {
		logger.Printf("%05d %s", i, payload)
	}
	if _, err := other.WriteString("child output\n"); err != nil {
		t.Fatal(err)
	}
	other.Close()
	logger.Print("last line")
	writer.Close()
	data, err := os.ReadFile(path)
	if err != nil || len(data) > 1<<20 {
		t.Fatalf("log exceeds its limit: %d bytes: %v", len(data), err)
	}
	lines := strings.Split(strings.TrimSuffix(string(data), "\n"), "\n")
	if !strings.HasPrefix(lines[0], "--- Earlier lines removed") {
		t.Fatalf("missing truncation marker: %.80q", lines[0])
	}
	for _, line := range lines[1:] {
		if !strings.HasSuffix(line, payload) && !strings.HasSuffix(line, "last line") && line != "child output" {
			t.Fatalf("partial line kept after truncation: %.80q", line)
		}
	}
	text := string(data)
	if !strings.Contains(text, "02999 ") || !strings.Contains(text, "\nchild output\n") || !strings.HasSuffix(text, "last line\n") || strings.Contains(text, " 00000 x") {
		t.Fatal("truncation must keep the newest lines and drop the oldest")
	}
}

func TestLogSizeAndRemovedLegacyConfig(t *testing.T) {
	for _, tc := range []struct{ name, from, to, want string }{
		{"size-zero", "max_size_mb = 10", "max_size_mb = 0", "logging.max_size_mb"},
		{"size-too-large", "max_size_mb = 10", "max_size_mb = 1025", "logging.max_size_mb"},
		{"size-valid", "max_size_mb = 10", "max_size_mb = 5", ""},
		{"legacy-backups", "max_size_mb = 10", "max_size_mb = 10\nbackups = 3", "unknown config setting"},
		{"legacy-health-target", "failures_before_failover = 3", "failures_before_failover = 3\ntarget = \"example.com:443\"", "unknown config setting"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			path, _ := testutil.Config(t, "Host test\n HostName example.invalid\n User tester\n")
			template := string(testutil.ConfigTemplate(t))
			if !strings.Contains(template, tc.from) {
				t.Fatal("default config changed:", tc.from)
			}
			if err := os.WriteFile(path, []byte(strings.Replace(template, tc.from, tc.to, 1)), 0600); err != nil {
				t.Fatal(err)
			}
			cfg, err := config.LoadConfig(path)
			if tc.want == "" {
				if err != nil || cfg.Logging.MaxSizeMB != 5 {
					t.Fatal("size limit not loaded", err)
				}
			} else if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("want error %q, got %v", tc.want, err)
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
			cfg, err := config.LoadConfig(path)
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
