//go:build systemtest

package testutil

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"testing"
	"time"

	"ssh-proxy/src/config"
	"ssh-proxy/src/control"
	"ssh-proxy/src/platform"
)

func SystemBuild(t *testing.T) string { t.Helper(); return os.Getenv("SSH_PROXY_SYS_DIR") }
func CLIPath(dir string) string {
	name := "ssh-proxy-cli"
	if runtime.GOOS == "windows" {
		name += ".exe"
	}
	return filepath.Join(dir, name)
}
func AppPath(dir string) string {
	if runtime.GOOS == "darwin" {
		return filepath.Join(dir, "ssh-proxy-app.app/Contents/MacOS/ssh-proxy-app")
	}
	return filepath.Join(dir, "ssh-proxy-app.exe")
}

// IsolatedBuild copies the build to a unique folder. Its unique path gives it
// its own installation identity, so its runtime folder, lock and login entries
// are separate from the user's and other tests', and tests can run in parallel.
func IsolatedBuild(t *testing.T) string {
	t.Helper()
	dir := filepath.Join(t.TempDir(), "app 中文-"+strconv.FormatInt(time.Now().UnixNano(), 36))
	if err := os.CopyFS(dir, os.DirFS(SystemBuild(t))); err != nil {
		t.Fatal(err)
	}
	p, err := config.PathsForExecutable(CLIPath(dir))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(p.Runtime) })
	t.Cleanup(func() {
		if t.Failed() {
			files, _ := filepath.Glob(filepath.Join(dir, "logs", "*.txt"))
			for _, file := range files {
				data := ReadFile(file)
				if len(data) > 4096 {
					data = data[len(data)-4096:]
				}
				t.Logf("%s:\n%s", file, data)
			}
		}
	})
	return dir
}
func CallCLI(dir string, args ...string) ([]byte, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, CLIPath(dir), args...)
	cmd.WaitDelay = time.Second
	platform.HideChild(cmd)
	return cmd.CombinedOutput()
}
func MustCLI(t *testing.T, dir string, args ...string) []byte {
	t.Helper()
	out, err := CallCLI(dir, args...)
	if err != nil {
		t.Fatalf("%v: %v: %s", args, err, out)
	}
	switch args[0] {
	case "status", "check":
		if !json.Valid(out) {
			t.Fatalf("%v must return one JSON report: %s", args, out)
		}
	case "start", "stop", "restart", "test", "-v", "--version", "autostart":
		if len(out) == 0 || json.Valid(out) {
			t.Fatalf("%v must return a text report: %s", args, out)
		}
	}
	return out
}
func StatusCLI(t *testing.T, dir string) control.Status {
	t.Helper()
	var st control.Status
	if err := json.Unmarshal(MustCLI(t, dir, "status"), &st); err != nil {
		t.Fatal(err)
	}
	return st
}
func WaitFor(t *testing.T, ready func() bool) {
	t.Helper()
	limit := time.Now().Add(20 * time.Second)
	for time.Now().Before(limit) {
		if ready() {
			return
		}
		time.Sleep(100 * time.Millisecond)
	}
	t.Fatal("timed out waiting for native program; see test logs and desktop permission requirements")
}
func ReadFile(path string) string { data, _ := os.ReadFile(path); return string(data) }
func WriteFile(t *testing.T, path, text string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(text), 0600); err != nil {
		t.Fatal(err)
	}
}
