package tests

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"ssh-proxy/src/config"
	"ssh-proxy/src/logs"
	"ssh-proxy/tests/helpers"
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

// When the size limit cuts the log while logs --follow runs, it prints a notice
// and keeps following the lines written after the cut.
func TestLogsFollowContinuesAfterSizeLimitCut(t *testing.T) {
	path := filepath.Join(t.TempDir(), "run.txt")
	logger, writer, err := logs.NewLog(path, false)
	if err != nil {
		t.Fatal(err)
	}
	defer writer.Close()
	writer.SetLimit(1)
	logger.Print("before the cut")
	reader, pipe, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	saved := os.Stdout
	os.Stdout = pipe
	defer func() { os.Stdout = saved }()
	var mu sync.Mutex
	var output strings.Builder
	go func() {
		buffer := make([]byte, 4096)
		for {
			n, err := reader.Read(buffer)
			mu.Lock()
			output.Write(buffer[:n])
			mu.Unlock()
			if err != nil {
				return
			}
		}
	}()
	shown := func() string { mu.Lock(); defer mu.Unlock(); return output.String() }
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	go func() { done <- logs.Show(ctx, path, 1, true) }()
	waitFor := func(ready func() bool) {
		t.Helper()
		for deadline := time.Now().Add(5 * time.Second); !ready(); time.Sleep(20 * time.Millisecond) {
			if time.Now().After(deadline) {
				t.Fatalf("follow output so far: %q", shown())
			}
		}
	}
	waitFor(func() bool { return strings.Contains(shown(), "before the cut") })
	payload := strings.Repeat("x", 1000)
	for i := 0; i < 1200; i++ {
		logger.Printf("%04d %s", i, payload) // Crosses 1 MB, so the file is cut.
	}
	waitFor(func() bool { return strings.Contains(shown(), "--- The log was cut") })
	// Lines written while the cut settles may be skipped; later ones are shown.
	line := 0
	waitFor(func() bool {
		line++
		logger.Printf("after the cut %d", line)
		return strings.Contains(shown(), "after the cut")
	})
	cancel()
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	pipe.Close()
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
