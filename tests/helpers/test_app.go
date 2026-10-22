//go:build systemtest && (windows || darwin)

package testutil

import (
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"ssh_proxy/src/proxy"
)

type NativeItem struct {
	Label                        string
	Disabled, Checked, Separator bool
}

func StartApp(t *testing.T, dir, config, backend string) *exec.Cmd {
	t.Helper()
	cmd := exec.Command(AppPath(dir), "--config", config, "--backend", backend)
	proxy.HideChild(cmd)
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	// Also reap setup children on a failed Windows test. The shipped app leaves
	// user setup terminals alone when it exits, but test fixtures must not leak.
	var release func()
	if runtime.GOOS == "windows" {
		var err error
		release, err = proxy.ContainChild(cmd)
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
func ExpectDialog(t *testing.T, pid int, reason string) string {
	t.Helper()
	var message string
	WaitFor(t, func() bool { message = NativeError(t, pid); return strings.Contains(message, reason) })
	return message
}
