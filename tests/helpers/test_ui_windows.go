//go:build systemtest

package testutil

import (
	"strings"
	"syscall"
	"testing"
	"unsafe"

	"github.com/lxn/win"
	"golang.org/x/sys/windows"
)

func FindNativeWindow(pid int, parent uintptr, class string) win.HWND {
	find := windows.NewLazySystemDLL("user32.dll").NewProc("FindWindowExW")
	var after uintptr
	name, _ := windows.UTF16PtrFromString(class)
	for {
		value, _, _ := find.Call(parent, after, uintptr(unsafe.Pointer(name)), 0)
		if value == 0 {
			return 0
		}
		var owner uint32
		win.GetWindowThreadProcessId(win.HWND(value), &owner)
		if int(owner) == pid {
			return win.HWND(value)
		}
		after = value
	}
}

func DialogText(dialog win.HWND) string {
	var text strings.Builder
	win.EnumChildWindows(dialog, syscall.NewCallback(func(hwnd win.HWND, _ uintptr) uintptr {
		var buffer [16384]uint16
		win.SendMessage(hwnd, win.WM_GETTEXT, uintptr(len(buffer)), uintptr(unsafe.Pointer(&buffer[0])))
		text.WriteString(syscall.UTF16ToString(buffer[:]))
		return 1
	}), 0)
	return text.String()
}

func DismissErrorDialog(t *testing.T, pid int, dialog win.HWND) {
	t.Helper()
	button := FindNativeWindow(pid, uintptr(dialog), "Button")
	if button == 0 || !win.IsWindowVisible(button) {
		t.Fatal("OK button missing")
	}
	// Use the native control's ID (Windows may map the sole dismiss button to
	// IDCANCEL). Deliver its notification without relying on foreground focus.
	controlID, _, _ := windows.NewLazySystemDLL("user32.dll").NewProc("GetDlgCtrlID").Call(uintptr(button))
	WaitFor(t, func() bool {
		if !win.IsWindowVisible(dialog) || FindNativeWindow(pid, 0, "#32770") != dialog {
			return true
		}
		if win.PostMessage(dialog, win.WM_COMMAND, controlID, uintptr(button)) == 0 {
			t.Fatal("click OK")
		}
		return false
	})
}
