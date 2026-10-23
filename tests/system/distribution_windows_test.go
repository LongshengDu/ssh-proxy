//go:build systemtest

package tests

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"
	"unsafe"

	"github.com/lxn/win"
	"golang.org/x/sys/windows"
	"golang.org/x/sys/windows/registry"

	"ssh_proxy/src/config"
	"ssh_proxy/src/platform"
	"ssh_proxy/src/ui"
	"ssh_proxy/tests/helpers"
)

// Windows-only integration; the menu itself is checked on every desktop OS by
// TestTrayNativeWorkflow.
func TestTrayWindowsIntegration(t *testing.T) {
	dir := testutil.IsolatedBuild(t)
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
	p, err := config.PathsForExecutable(testutil.CLIPath(dir))
	if err != nil {
		t.Fatal(err)
	}
	// A configured server keeps first-launch setup from opening ssh_config.
	testutil.WriteFile(t, filepath.Join(dir, "ssh_config"), "Host proxy-primary\n HostName example.invalid\n User tester\n")
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
	var hwnd win.HWND
	for deadline := time.Now().Add(10 * time.Second); time.Now().Before(deadline) && hwnd == 0; time.Sleep(100 * time.Millisecond) {
		hwnd = testutil.FindNativeWindow(tray.Process.Pid, ^uintptr(2), "GoGPUSystrayMsg")
	}
	if hwnd == 0 {
		t.Fatal("gogpu/systray did not create its native window")
	}
	t.Cleanup(func() {
		if err := ui.SetStartup(p.ID, "", false); err != nil {
			t.Errorf("startup cleanup: %v", err)
		}
	})
	testutil.NativeClick(t, tray.Process.Pid, "Launch App at Login")
	testutil.WaitFor(t, func() bool { enabled, err := ui.StartupEnabled(p.ID); return err == nil && enabled })
	key, err := registry.OpenKey(registry.CURRENT_USER, `Software\Microsoft\Windows\CurrentVersion\Run`, registry.QUERY_VALUE)
	if err != nil {
		t.Fatal(err)
	}
	command, _, err := key.GetStringValue("SSHProxy-" + p.ID)
	key.Close()
	// The default config needs no argument; Windows lists the app itself, not PowerShell.
	if want := windows.ComposeCommandLine([]string{filepath.Join(dir, "ssh_proxy_app.exe")}); err != nil || command != want {
		t.Fatalf("invalid tray login command: %v: %s; want %s", err, command, want)
	}
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
