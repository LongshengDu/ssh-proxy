//go:build systemtest

package tests

import (
	"os/exec"
	"path/filepath"
	"testing"
	"time"
	"unsafe"

	"github.com/lxn/win"
	"golang.org/x/sys/windows"

	"ssh-proxy/src/platform"
	"ssh-proxy/tests/helpers"
)

// Windows-only integration: the embedded icon and native shutdown. The menu,
// including Launch App at Login, is checked on every desktop OS by the other
// tray tests.
func TestTrayWindowsIntegration(t *testing.T) {
	dir := testutil.IsolatedBuild(t)
	iconPath, _ := windows.UTF16PtrFromString(filepath.Join(dir, "ssh-proxy-app.exe"))
	icons, _, _ := windows.NewLazySystemDLL("shell32.dll").NewProc("ExtractIconExW").Call(uintptr(unsafe.Pointer(iconPath)), ^uintptr(0), 0, 0, 0)
	if icons != 1 {
		t.Fatalf("expected one embedded application icon, got %d", icons)
	}
	// A configured server keeps first-launch setup from opening ssh_config.
	testutil.WriteFile(t, filepath.Join(dir, "ssh_config"), "Host proxy-primary\n HostName example.invalid\n User tester\n")
	tray := exec.Command(filepath.Join(dir, "ssh-proxy-app.exe"))
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
