//go:build systemtest && (windows || darwin)

package testutil

import (
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"ssh-proxy/src/control"
	"ssh-proxy/src/platform"
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

// ExpectMenuLayout compares the whole native menu with the documented layout:
// item order, separators, disabled status rows and the choices that depend on
// whether the proxy runs (running is nil while stopped). Start Proxy When App
// Launches is expected at its default, unchecked.
func ExpectMenuLayout(t *testing.T, pid int, scripts []string, running *control.Status, startup bool) {
	t.Helper()
	phase, toggle, ssh, socks, http, exit := "○ Stopped", "Start proxy", "SSH: —", "SOCKS5: —", "HTTP: —", "Exit app"
	if running != nil {
		_, hostPort, _ := strings.Cut(running.Destination, "@")
		phase, toggle, ssh, socks, http, exit = "● Running", "Stop proxy", "SSH: "+hostPort, "SOCKS5: "+running.SOCKS, "HTTP: "+running.HTTP, "Exit app: Keep proxy running"
	}
	want := []string{phase, ssh, socks, http, "", toggle, ""}
	for _, script := range scripts {
		want = append(want, "Run: "+script)
	}
	if len(scripts) != 0 {
		want = append(want, "")
	}
	want = append(want, "Launch App at Login", "Start Proxy When App Launches", "", "Open SSH Config", "Open App Config", "",
		"Open Current Log", "Open Installed Location", "", "Exit app: Stop proxy", exit)
	// Wait until the rows that change with the proxy state have settled.
	ExpectMenu(t, pid, toggle, false, false)
	ExpectMenu(t, pid, http, true, false)
	ExpectMenu(t, pid, "Launch App at Login", false, startup)
	items := NativeMenu(t, pid)
	if len(items) != len(want) {
		t.Fatalf("menu has %d items, want %d: %+v", len(items), len(want), items)
	}
	byLabel := map[string]NativeItem{}
	for index, item := range items {
		if item.Label != want[index] || item.Separator != (want[index] == "") {
			t.Fatalf("menu item %d: got %+v, want %q", index, item, want[index])
		}
		byLabel[item.Label] = item
	}
	for _, label := range []string{phase, ssh, socks, http} {
		if !byLabel[label].Disabled {
			t.Fatal("status row is clickable:", label)
		}
	}
	for _, script := range scripts {
		if byLabel["Run: "+script].Disabled != (running == nil) {
			t.Fatal("script availability does not match the proxy state:", script)
		}
	}
	if byLabel[toggle].Checked || byLabel["Exit app: Stop proxy"].Disabled != (running == nil) || byLabel[exit].Disabled {
		t.Fatalf("proxy and exit choices do not match the proxy state: %+v", items)
	}
	if byLabel["Start Proxy When App Launches"].Checked {
		t.Fatal("Start Proxy When App Launches must default to off")
	}
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
