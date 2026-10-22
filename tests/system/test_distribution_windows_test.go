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
	"syscall"
	"testing"
	"time"
	"unsafe"

	"github.com/lxn/win"
	"golang.org/x/sys/windows"
	"golang.org/x/sys/windows/registry"

	"ssh_proxy/src/proxy"
	"ssh_proxy/src/ui"
	"ssh_proxy/tests/helpers"
)

func TestTrayWindowsMenu(t *testing.T) {
	dir := testutil.IsolatedBuild(t)
	call := func(exe string, args ...string) ([]byte, error) {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		cmd := exec.CommandContext(ctx, exe, args...)
		proxy.HideChild(cmd)
		return cmd.CombinedOutput()
	}
	iconPath := syscall.StringToUTF16Ptr(filepath.Join(dir, "ssh_proxy_app.exe"))
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
	p, err := proxy.PathsForExecutable(filepath.Join(dir, "config.toml"), exe)
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
	proxy.HideChild(tray)
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
		win.PostMessage(hwnd, win.WM_USER+100, 0, win.WM_RBUTTONUP)
		var popup win.HWND
		limit := time.Now().Add(5 * time.Second)
		for time.Now().Before(limit) && popup == 0 {
			popup = findOwned(0, "#32768")
			time.Sleep(50 * time.Millisecond)
		}
		if popup == 0 {
			t.Fatal("native context menu did not open")
		}
		defer func() {
			win.PostMessage(hwnd, win.WM_CANCELMODE, 0, 0)
			limit := time.Now().Add(3 * time.Second)
			for findOwned(0, "#32768") != 0 && time.Now().Before(limit) {
				time.Sleep(50 * time.Millisecond)
			}
			if findOwned(0, "#32768") != 0 {
				t.Error("menu did not dismiss")
			}
		}()
		menu := win.HMENU(win.SendMessage(popup, 0x01e1, 0, 0)) // MN_GETHMENU
		if count := win.GetMenuItemCount(menu); count != 19 {
			t.Fatalf("menu has %d items", count)
		}
		phase, control, ssh, socks, http := "○ Stopped", "Start proxy", "SSH: —", "SOCKS5: —", "HTTP: —"
		if running {
			phase, control, ssh, socks, http = "● Running", "Stop proxy", "SSH: tester@example.invalid:2222 (primary)", "SOCKS5: 127.0.0.1:1088", "HTTP: 127.0.0.1:8088"
		}
		want := []string{phase, ssh, socks, http, "", control, "", "Run: Claude", "Run: Codex", "", "Launch App at Login", "", "Open SSH Config", "Open App Config", "", "Open Current Log", "Open Installed Location", "", "Exit app (proxy keeps running)"}
		for index := uint32(0); index < uint32(len(want)); index++ {
			var label [256]uint16
			info := win.MENUITEMINFO{FMask: win.MIIM_STRING | win.MIIM_STATE | win.MIIM_FTYPE, DwTypeData: &label[0], Cch: 255}
			info.CbSize = uint32(unsafe.Sizeof(info))
			if !win.GetMenuItemInfo(menu, index, 1, &info) {
				t.Fatal("read native menu")
			}
			name := syscall.UTF16ToString(label[:])
			t.Logf("menu %d: %s (state=%d)", index, name, info.FState)
			if name != want[index] {
				t.Fatalf("menu %d: got %q, want %q", index, name, want[index])
			}
			if (info.FType&0x800 != 0) != (want[index] == "") {
				t.Fatal("missing native separator", index)
			}
			if index < 4 && info.FState&win.MFS_DISABLED == 0 {
				t.Fatal("status row is clickable", index)
			}
			if index == 5 && info.FState&win.MFS_CHECKED != 0 {
				t.Fatal("proxy action has a checkbox")
			}
			if (index == 7 || index == 8) && (info.FState&win.MFS_DISABLED != 0) == running {
				t.Fatal("script readiness out of sync")
			}
			if index == 10 && (info.FState&win.MFS_CHECKED != 0) != wantStartup {
				t.Fatal("startup checkbox out of sync")
			}
		}
	}
	menuState(false, false)
	t.Cleanup(func() {
		if err := ui.SetStartup(p.ID, "", "", false); err != nil {
			t.Errorf("startup cleanup: %v", err)
		}
	})
	testutil.ClickTrayItem(t, tray.Process.Pid, 10)
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
	if err != nil || len(command) > 260 || !strings.Contains(command, "-WindowStyle Hidden") || !strings.Contains(command, "$sshProxy.Executable --config $sshProxy.Config") {
		t.Fatalf("invalid tray login command: %v: %s", err, command)
	}
	time.Sleep(2300 * time.Millisecond)
	menuState(true, false)
	testutil.ClickTrayItem(t, tray.Process.Pid, 10)
	testutil.WaitFor(t, func() bool { enabled, err := ui.StartupEnabled(p.ID); return err == nil && !enabled })
	time.Sleep(2300 * time.Millisecond)
	menuState(false, false)
	// Publish an authenticated backend status fixture. The real CLI reads it,
	// and the real app must reflect it without owning a separate proxy state.
	token := strings.Repeat("a", 64)
	server := httptest.NewServer(proxy.ControlHandler(token, func() proxy.Status {
		return proxy.Status{PID: os.Getpid(), Config: p.Config, Phase: "running", Endpoint: "primary", Destination: "tester@example.invalid:2222", SOCKS: "127.0.0.1:1088", HTTP: "127.0.0.1:8088", Log: p.Log}
	}, func() {}))
	defer server.Close()
	state, _ := json.Marshal(proxy.StateFile{PID: os.Getpid(), Config: p.Config, Token: token, Address: strings.TrimPrefix(server.URL, "http://")})
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
	for proxy.ProcessAlive(tray.Process.Pid) && time.Now().Before(limit) {
		time.Sleep(100 * time.Millisecond)
	}
	if proxy.ProcessAlive(tray.Process.Pid) {
		t.Fatal("tray did not exit")
	}
	// Release the process handle/image reference.
	if err := tray.Wait(); err != nil {
		t.Fatalf("tray exit: %v", err)
	}
}
