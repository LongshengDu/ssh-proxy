//go:build systemtest && (windows || darwin)

package testutil

import (
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"ssh_proxy/src/platform"
)

type NativeItem struct {
	Label                        string
	Disabled, Checked, Separator bool
}

func StartApp(t *testing.T, dir string, args ...string) *exec.Cmd {
	t.Helper()
	cmd := exec.Command(AppPath(dir), args...)
	platform.HideChild(cmd)
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	// Also reap setup children on a failed Windows test. The shipped app leaves
	// user setup terminals alone when it exits, but test fixtures must not leak.
	var release func()
	if runtime.GOOS == "windows" {
		var err error
		release, err = platform.ContainChild(cmd)
		if err != nil {
			cmd.Process.Kill()
			cmd.Wait()
			t.Fatal(err)
		}
	}
	t.Cleanup(func() {
		if release != nil {
			release()
		}
		if cmd.ProcessState == nil {
			cmd.Process.Kill()
			cmd.Wait()
		}
	})
	return cmd
}
func TrayLog(dir string) string {
	files, _ := filepath.Glob(filepath.Join(dir, "logs", "tray_*.txt"))
	if len(files) == 0 {
		return ""
	}
	return files[len(files)-1]
}
func ExpectMenu(t *testing.T, pid int, label string, disabled, checked bool) {
	t.Helper()
	var last []NativeItem
	defer func() {
		if t.Failed() {
			t.Logf("waiting for %q (disabled=%v, checked=%v); last menu: %+v", label, disabled, checked, last)
		}
	}()
	// Backend readiness can precede the action-complete event that re-enables
	// menu items. Wait for the native menu's complete state, not just its label.
	WaitFor(t, func() bool {
		last = NativeMenu(t, pid)
		for _, item := range last {
			if item.Label == label {
				return item.Disabled == disabled && item.Checked == checked
			}
		}
		return false
	})
}

// ExpectNoDialog fails if the app shows an error dialog within two seconds.
func ExpectNoDialog(t *testing.T, pid int) {
	t.Helper()
	for deadline := time.Now().Add(2 * time.Second); time.Now().Before(deadline); time.Sleep(200 * time.Millisecond) {
		if message := NativeError(t, pid); message != "" {
			t.Fatal("unexpected error dialog:", message)
		}
	}
}

func ExpectDialog(t *testing.T, pid int, reason string) string {
	t.Helper()
	var message string
	WaitFor(t, func() bool { message = NativeError(t, pid); return strings.Contains(message, reason) })
	return message
}
