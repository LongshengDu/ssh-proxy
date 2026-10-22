//go:build systemtest

package tests

import (
	"context"
	"encoding/json"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
	"unsafe"

	"github.com/lxn/win"
	"golang.org/x/sys/windows"
	"golang.org/x/sys/windows/registry"

	"ssh_proxy/src/config"
	"ssh_proxy/src/control"
	"ssh_proxy/src/platform"
	"ssh_proxy/src/ui"
	"ssh_proxy/tests/helpers"
)

func TestTrayWindowsMenu(t *testing.T) {
	dir := testutil.IsolatedBuild(t)
	call := func(exe string, args ...string) ([]byte, error) {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		cmd := exec.CommandContext(ctx, exe, args...)
		platform.HideChild(cmd)
		return cmd.CombinedOutput()
	}
	iconPath, _ := windows.UTF16PtrFromString(filepath.Join(dir, "ssh_proxy_app.exe"))
	icons, _, _ := windows.NewLazySystemDLL("shell32.dll").NewProc("ExtractIconExW").Call(uintptr(unsafe.Pointer(iconPath)), ^uintptr(0), 0, 0, 0)
	if icons != 1 {
		t.Fatalf("expected one embedded application icon, got %d", icons)
	}
	if logs, err := os.ReadDir(filepath.Join(dir, "logs")); err != nil || len(logs) != 0 {
		t.Fatal("logs payload not empty", err)
	}
	if _, err := os.Stat(filepath.Join(dir, "known_hosts")); !os.IsNotExist(err) {
		t.Fatal("private host keys distributed")
	}
	exe := filepath.Join(dir, "ssh_proxy_cli.exe")
	t.Setenv("SSH_PROXY_RUNTIME", t.TempDir())
	p, err := config.PathsForExecutable(filepath.Join(dir, "config.toml"), exe)
	if err != nil {
		t.Fatal(err)
	}
	if out, err := call(exe, "check"); err != nil || !json.Valid(out) || !strings.Contains(string(out), "add your hosts") {
		t.Fatalf("fresh installation should require endpoint configuration: %v: %s", err, out)
	}
	// Populate only this isolated installation, as a user does after extraction.
	if err = os.WriteFile(filepath.Join(dir, "config.toml"), testutil.ConfigTemplate(t), 0600); err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(filepath.Join(dir, "ssh_config"), []byte("Host proxy-primary proxy-backup\n HostName example.invalid\n User tester\n Port 22\n"), 0600); err != nil {
		t.Fatal(err)
	}
	for _, args := range [][]string{{"check"}, {"status"}} {
		if out, err := call(exe, args...); err != nil {
			t.Fatalf("%v: %v: %s", args, err, out)
		} else {
			t.Logf("%v: %s", args, out)
		}
	}
	tray := exec.Command(filepath.Join(dir, "ssh_proxy_app.exe"))
	platform.HideChild(tray)
	if err := tray.Start(); err != nil {
		t.Fatal(err)
	}
	defer func() {
		if tray.ProcessState == nil {
			tray.Process.Kill()
			tray.Wait()
		}
	}()
	findOwned := func(parent uintptr, class string) win.HWND {
		return testutil.FindNativeWindow(tray.Process.Pid, parent, class)
	}
	var hwnd win.HWND
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) && hwnd == 0 {
		hwnd = findOwned(^uintptr(2), "GoGPUSystrayMsg")
		time.Sleep(100 * time.Millisecond)
	}
	if hwnd == 0 {
		t.Fatal("gogpu/systray did not create its native window")
	}
	time.Sleep(500 * time.Millisecond)
	// Read the actual native menu (not just the application model).
	menuState := func(wantStartup, running bool) {
		t.Helper()
		items := testutil.NativeMenu(t, tray.Process.Pid)
		phase, toggle, ssh, socks, http := "○ Stopped", "Start proxy", "SSH: —", "SOCKS5: —", "HTTP: —"
		if running {
			phase, toggle, ssh, socks, http = "● Running", "Stop proxy", "SSH: tester@example.invalid:2222 (primary)", "SOCKS5: 127.0.0.1:1088", "HTTP: 127.0.0.1:8088"
		}
		exit := "Exit app"
		if running {
			exit = "Keep proxy running and exit"
		}
		want := []string{phase, ssh, socks, http, "", toggle, "", "Run: Claude", "Run: Codex", "", "Launch App at Login", "Start Proxy When App Launches", "", "Open SSH Config", "Open App Config", "", "Open Current Log", "Open Installed Location", "", "Stop proxy and exit", exit}
		if len(items) != len(want) {
			t.Fatalf("menu has %d items: %+v", len(items), items)
		}
		byLabel := map[string]testutil.NativeItem{}
		for index, item := range items {
			t.Logf("menu %d: %+v", index, item)
			if item.Label != want[index] || item.Separator != (want[index] == "") {
				t.Fatalf("menu %d: got %+v, want %q", index, item, want[index])
			}
			byLabel[item.Label] = item
		}
		for _, label := range []string{phase, ssh, socks, http} {
			if !byLabel[label].Disabled {
				t.Fatal("status row is clickable", label)
			}
		}
		if byLabel[toggle].Checked {
			t.Fatal("proxy action has a checkbox")
		}
		if byLabel["Stop proxy and exit"].Disabled == running || byLabel[exit].Disabled {
			t.Fatal("exit choices do not match the proxy state")
		}
		for _, label := range []string{"Run: Claude", "Run: Codex"} {
			if byLabel[label].Disabled == running {
				t.Fatal("script readiness out of sync", label)
			}
		}
		if byLabel["Launch App at Login"].Checked != wantStartup {
			t.Fatal("startup checkbox out of sync")
		}
		if byLabel["Start Proxy When App Launches"].Checked {
			t.Fatal("the public default must not start the proxy at app launch")
		}
	}
	menuState(false, false)
	t.Cleanup(func() {
		if err := ui.SetStartup(p.ID, "", "", false); err != nil {
			t.Errorf("startup cleanup: %v", err)
		}
	})
	testutil.NativeClick(t, tray.Process.Pid, "Launch App at Login")
	testutil.WaitFor(t, func() bool { enabled, err := ui.StartupEnabled(p.ID); return err == nil && enabled })
	key, err := registry.OpenKey(registry.CURRENT_USER, `Software\SSHProxy\`+p.ID, registry.QUERY_VALUE)
	if err != nil {
		t.Fatal(err)
	}
	registered, _, err := key.GetStringValue("Executable")
	key.Close()
	if err != nil || registered != filepath.Join(dir, "ssh_proxy_app.exe") {
		t.Fatal("login must launch only the app", registered, err)
	}
	key, err = registry.OpenKey(registry.CURRENT_USER, `Software\Microsoft\Windows\CurrentVersion\Run`, registry.QUERY_VALUE)
	if err != nil {
		t.Fatal(err)
	}
	command, _, err := key.GetStringValue("SSHProxy-" + p.ID)
	key.Close()
	// The default config needs no argument; Windows lists the app itself, not PowerShell.
	if want := windows.ComposeCommandLine([]string{filepath.Join(dir, "ssh_proxy_app.exe")}); err != nil || command != want {
		t.Fatalf("invalid tray login command: %v: %s; want %s", err, command, want)
	}
	time.Sleep(2300 * time.Millisecond)
	menuState(true, false)
	testutil.NativeClick(t, tray.Process.Pid, "Launch App at Login")
	testutil.WaitFor(t, func() bool { enabled, err := ui.StartupEnabled(p.ID); return err == nil && !enabled })
	time.Sleep(2300 * time.Millisecond)
	menuState(false, false)
	// Publish an authenticated backend status fixture. The real CLI reads it,
	// and the real app must reflect it without owning a separate proxy state.
	token := strings.Repeat("a", 64)
	server := httptest.NewServer(control.ControlHandler(token, func() control.Status {
		return control.Status{PID: os.Getpid(), Config: p.Config, Phase: "running", Endpoint: "primary", Destination: "tester@example.invalid:2222", SOCKS: "127.0.0.1:1088", HTTP: "127.0.0.1:8088", Log: p.Log}
	}, func() {}))
	defer server.Close()
	state, _ := json.Marshal(control.StateFile{PID: os.Getpid(), Config: p.Config, Token: token, Address: strings.TrimPrefix(server.URL, "http://")})
	if err := os.WriteFile(p.State, state, 0600); err != nil {
		t.Fatal(err)
	}
	time.Sleep(2300 * time.Millisecond)
	menuState(false, true)
	if err := os.Remove(p.State); err != nil {
		t.Fatal(err)
	}
	time.Sleep(2300 * time.Millisecond)
	menuState(false, false) // No stale PID, destination or addresses after stop.
	win.PostMessage(hwnd, win.WM_CLOSE, 0, 0)
	// Native-library shutdown must release the application process.
	limit := time.Now().Add(5 * time.Second)
	for platform.ProcessAlive(tray.Process.Pid) && time.Now().Before(limit) {
		time.Sleep(100 * time.Millisecond)
	}
	if platform.ProcessAlive(tray.Process.Pid) {
		t.Fatal("tray did not exit")
	}
	// Release the process handle/image reference.
	if err := tray.Wait(); err != nil {
		t.Fatalf("tray exit: %v", err)
	}
}
