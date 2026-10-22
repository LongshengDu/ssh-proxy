//go:build systemtest

package tests

import (
	"context"
	"encoding/json"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"ssh_proxy/src/proxy"
	"ssh_proxy/tests/helpers"
)

// Real child-process logging, using only local closed ports and no SSH credentials.
func TestProxyRunLogs(t *testing.T) {
	dir := testutil.IsolatedBuild(t)
	reserve := func() net.Listener {
		ln, err := net.Listen("tcp", "127.0.0.1:0")
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { ln.Close() })
		return ln
	}
	socks, http, ssh := reserve(), reserve(), reserve()
	port := func(ln net.Listener) string { return strconv.Itoa(ln.Addr().(*net.TCPAddr).Port) }
	text := strings.Replace(string(testutil.ConfigTemplate(t)), "max_log_files = 10", "max_log_files = 2", 1)
	text = strings.ReplaceAll(text, "socks_port = 1081", "socks_port = "+port(socks))
	text = strings.ReplaceAll(text, "http_port = 8181", "http_port = "+port(http))
	if err := os.WriteFile(filepath.Join(dir, "config.toml"), []byte(text), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "ssh_config"), []byte("Host local-test\n HostName 127.0.0.1\n User test\n Port "+port(ssh)+"\n"), 0600); err != nil {
		t.Fatal(err)
	}
	http.Close()
	ssh.Close()
	call := func(args ...string) ([]byte, error) {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		cmd := exec.CommandContext(ctx, testutil.CLIPath(dir), args...)
		proxy.HideChild(cmd)
		return cmd.CombinedOutput()
	}
	t.Cleanup(func() {
		if out, err := call("stop"); err != nil {
			t.Errorf("cleanup: %v: %s", err, out)
		}
	})
	oldProxy := testutil.SeedRunLogs(t, filepath.Join(dir, "logs"), "logs", 3)
	oldTray := testutil.SeedRunLogs(t, filepath.Join(dir, "logs"), "tray", 3)
	files := func(want int) {
		t.Helper()
		found, err := filepath.Glob(filepath.Join(dir, "logs", "logs_*.txt"))
		if err != nil || len(found) != want {
			t.Fatalf("wanted %d proxy logs, got %d: %v", want, len(found), err)
		}
		for _, path := range oldTray {
			if _, err := os.Stat(path); err != nil {
				t.Fatal("proxy startup removed a tray log", err)
			}
		}
	}
	// An early supervisor exit must be reported at once, not after the whole wait.
	began := time.Now()
	if out, err := call("start", "--wait", "25"); err == nil || !strings.Contains(string(out), "SOCKS port is unavailable") {
		t.Fatalf("expected startup failure: %v: %s", err, out)
	}
	if elapsed := time.Since(began); elapsed > 10*time.Second {
		t.Fatalf("startup failure took %v; the exited supervisor was not detected", elapsed)
	}
	files(2)
	first := testutil.StatusCLI(t, dir).Log
	initial, _ := os.ReadFile(first)
	if !strings.Contains(string(initial), "Starting ssh_proxy_cli") || !strings.Contains(string(initial), "SOCKS port is unavailable") {
		t.Fatal("bootstrap failure split from run log")
	}
	socks.Close()
	if out, err := call("start", "--wait", "1"); err != nil {
		t.Fatalf("start retrying supervisor: %v: %s", err, out)
	}
	files(2)
	var status proxy.Status
	out, err := call("status")
	if err != nil || json.Unmarshal(out, &status) != nil || status.PID == 0 || status.Log == first {
		t.Fatalf("current run not discoverable: %v: %s", err, out)
	}
	if out, err := call("start", "--wait", "1"); err != nil {
		t.Fatalf("repeat start: %v: %s", err, out)
	}
	files(2)
	if out, err := call("stop"); err != nil {
		t.Fatalf("stop: %v: %s", err, out)
	}
	files(2)
	for _, path := range oldProxy {
		if _, err := os.Stat(path); !os.IsNotExist(err) {
			t.Fatal("old proxy log not pruned", path, err)
		}
	}
	final, _ := os.ReadFile(status.Log)
	if !strings.Contains(string(final), "Starting ssh_proxy_cli") || !strings.Contains(string(final), "Stopped; SSH child") {
		t.Fatal("start/stop history was not kept in one file")
	}
	if out, err := call("logs", "--lines", "100"); err != nil || !strings.Contains(string(out), "Stopped; SSH child") {
		t.Fatalf("logs did not find completed run: %v: %s", err, out)
	}
}
