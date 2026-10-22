package tests

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"
	"time"

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

func TestRetentionKeepsActiveLogs(t *testing.T) {
	dir := t.TempDir()
	p := config.Paths{Config: filepath.Join(dir, "config.toml"), Logs: dir, ID: "abcdef0123456789"}
	if err := os.WriteFile(p.Config, []byte("[logging]\nmax_log_files = 1\n"), 0600); err != nil {
		t.Fatal(err)
	}
	for _, kind := range []string{"logs", "tray"} {
		logger, active, err := logs.NewRunLog(p, kind, false)
		if err != nil {
			t.Fatal(err)
		}
		defer active.Close()
		_, other, err := logs.NewRunLog(p, kind, false)
		if err != nil {
			t.Fatal(err)
		}
		other.Close()
		logger.Print("still visible")
		if got, err := os.ReadFile(active.Name()); err != nil || !strings.Contains(string(got), "still visible") {
			t.Fatal("active log was unlinked", err)
		}
		_, newest, err := logs.NewRunLog(p, kind, false)
		if err != nil {
			t.Fatal(err)
		}
		newest.Close()
		if _, err := os.Stat(other.Name()); !os.IsNotExist(err) {
			t.Fatal("closed log was not pruned", err)
		}
	}
}

// While another process appends (interactive trust and native ssh -E write the
// tray log), the owner must not cut the file, or their lines could be lost.
func TestRunLogHoldDefersTruncation(t *testing.T) {
	path := filepath.Join(t.TempDir(), "run.txt")
	logger, writer, err := logs.NewLog(path, false)
	if err != nil {
		t.Fatal(err)
	}
	defer writer.Close()
	writer.SetLimit(1)
	payload := strings.Repeat("x", 1000)
	for i := 0; i < 900; i++ {
		logger.Printf("before %04d %s", i, payload)
	}
	release := writer.Hold()
	other, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0600)
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 300; i++ {
		fmt.Fprintf(other, "external %04d %s\n", i, payload)
		logger.Printf("held %04d %s", i, payload)
	}
	other.Close()
	if data, _ := os.ReadFile(path); len(data) <= 1<<20 || strings.Contains(string(data), "Earlier lines removed") {
		t.Fatalf("log was cut while held: %d bytes", len(data))
	}
	release()
	release() // Releasing twice is harmless.
	logger.Print("after release")
	data, _ := os.ReadFile(path)
	text := string(data)
	if len(data) > 1<<20 || !strings.HasPrefix(text, "--- Earlier lines removed") {
		t.Fatalf("the first write after release must apply the limit: %d bytes", len(data))
	}
	if !strings.Contains(text, "external 0299 ") || !strings.Contains(text, "held 0299 ") || !strings.HasSuffix(text, "after release\n") {
		t.Fatal("the newest lines from every writer must be kept")
	}
}

func TestRunLogWithoutLimitNeverTruncates(t *testing.T) {
	path := filepath.Join(t.TempDir(), "run.txt")
	logger, writer, err := logs.NewLog(path, false)
	if err != nil {
		t.Fatal(err)
	}
	defer writer.Close()
	writer.SetLimit(1)
	writer.SetLimit(0) // Appending to a log that another process owns.
	payload := strings.Repeat("x", 1000)
	for i := 0; i < 1500; i++ {
		logger.Printf("%04d %s", i, payload)
	}
	if data, _ := os.ReadFile(path); len(data) <= 1<<20 || strings.Contains(string(data), "Earlier lines removed") {
		t.Fatalf("a log without a limit was cut: %d bytes", len(data))
	}
}

// logs --follow must continue right after the last line it showed when the size
// limit cuts the file between two polls: nothing skipped, nothing repeated.
func TestLogsFollowContinuesAfterSizeLimitCut(t *testing.T) {
	path := filepath.Join(t.TempDir(), "run.txt")
	logger, writer, err := logs.NewLog(path, false)
	if err != nil {
		t.Fatal(err)
	}
	defer writer.Close()
	writer.SetLimit(1)
	payload := strings.Repeat("x", 200)
	line := 0
	write := func(count int) {
		for range count {
			line++
			logger.Printf("line %06d %s", line, payload)
		}
	}
	write(2500)
	shownFrom := line // Show prints only the last existing line.
	reader, pipe, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	saved := os.Stdout
	os.Stdout = pipe
	var output bytes.Buffer
	copied := make(chan struct{})
	go func() { io.Copy(&output, reader); close(copied) }()
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- logs.Show(ctx, path, 1, true) }()
	time.Sleep(700 * time.Millisecond)
	write(100) // Followed normally.
	time.Sleep(700 * time.Millisecond)
	write(2500) // Crosses 1 MB, so the file is cut between two polls.
	time.Sleep(1300 * time.Millisecond)
	cancel()
	err = <-done
	os.Stdout = saved
	pipe.Close()
	<-copied
	if err != nil {
		t.Fatal(err)
	}
	if data, _ := os.ReadFile(path); !strings.HasPrefix(string(data), "--- Earlier lines removed") {
		t.Fatal("the test did not cut the log")
	}
	var shown []int
	for _, match := range regexp.MustCompile(`line (\d{6}) `).FindAllStringSubmatch(output.String(), -1) {
		number, _ := strconv.Atoi(match[1])
		shown = append(shown, number)
	}
	for i, number := range shown {
		if number != shownFrom+i {
			t.Fatalf("followed output skipped or repeated lines at %d: got line %d, want %d", i, number, shownFrom+i)
		}
	}
	if len(shown) != line-shownFrom+1 || strings.Contains(output.String(), "before they could be shown") {
		t.Fatalf("followed %d lines, want %d", len(shown), line-shownFrom+1)
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

func TestLogSizeAndRemovedSettings(t *testing.T) {
	for _, tc := range []struct{ name, from, to, want string }{
		{"size-zero", "max_size_mb = 10", "max_size_mb = 0", "logging.max_size_mb"},
		{"size-too-large", "max_size_mb = 10", "max_size_mb = 1025", "logging.max_size_mb"},
		{"size-valid", "max_size_mb = 10", "max_size_mb = 5", ""},
		{"removed-backups", "max_size_mb = 10", "max_size_mb = 5\nbackups = 3", "unknown config setting(s): [logging.backups]"},
		{"removed-health-target", "[health]", "[health]\ntarget = \"legacy.example:443\"", "unknown config setting(s): [health.target]"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			path, _ := testutil.Config(t, "Host test\n HostName example.invalid\n User tester\n")
			template := string(testutil.ConfigTemplate(t))
			if !strings.Contains(template, tc.from) {
				t.Fatal("default config changed:", tc.from)
			}
			text := strings.Replace(template, tc.from, tc.to, 1)
			if err := os.WriteFile(path, []byte(text), 0600); err != nil {
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
