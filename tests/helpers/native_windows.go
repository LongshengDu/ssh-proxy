//go:build systemtest

package testutil

import (
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"
	"unsafe"

	"github.com/lxn/win"
	"golang.org/x/sys/windows"
	"golang.org/x/sys/windows/registry"

	"ssh-proxy/src/platform"
)

func NativeLoginLaunch(t *testing.T, id, executable string) {
	t.Helper()
	key, err := registry.OpenKey(registry.CURRENT_USER, `Software\Microsoft\Windows\CurrentVersion\Run`, registry.QUERY_VALUE)
	if err != nil {
		t.Fatal(err)
	}
	command, _, err := key.GetStringValue("SSHProxy-" + id)
	key.Close()
	if err != nil {
		t.Fatal(err)
	}
	// Run the registered command line exactly as Windows does at sign-in.
	args, err := windows.DecomposeCommandLine(command)
	if err != nil || len(args) == 0 {
		t.Fatal("invalid login command", command, err)
	}
	target := args[0]
	if strings.EqualFold(filepath.Base(target), "powershell.exe") {
		settings, err := registry.OpenKey(registry.CURRENT_USER, `Software\SSHProxy\App-`+id, registry.QUERY_VALUE)
		if err != nil {
			t.Fatal(err)
		}
		target, _, err = settings.GetStringValue("Executable")
		settings.Close()
		if err != nil {
			t.Fatal(err)
		}
	}
	if target != executable {
		t.Fatalf("login executable = %q, want %q", target, executable)
	}
	cmd := exec.Command(args[0])
	cmd.SysProcAttr = &syscall.SysProcAttr{CmdLine: command, HideWindow: true}
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	release, err := platform.ContainChild(cmd)
	if err != nil {
		cmd.Process.Kill()
		cmd.Wait()
		t.Fatal(err)
	}
	t.Cleanup(func() { release(); cmd.Process.Kill(); cmd.Wait() })
}

var desktopWindows map[win.HWND]string

func NativeDesktop(t *testing.T) {
	t.Helper()
	desktopWindows = windowTitles()
}

// Inspect only console children of this test app, then write console key events
// only after verifying our loopback server's fingerprint. No global keystrokes.
func NativeAcceptHost(t *testing.T, appPID int, _ map[string]bool, fingerprint string) {
	t.Helper()
	var consolePID uint32
	WaitFor(t, func() bool {
		snapshot, err := windows.CreateToolhelp32Snapshot(windows.TH32CS_SNAPPROCESS, 0)
		if err != nil {
			t.Fatal(err)
		}
		defer windows.CloseHandle(snapshot)
		entry := windows.ProcessEntry32{Size: uint32(unsafe.Sizeof(windows.ProcessEntry32{}))}
		for err := windows.Process32First(snapshot, &entry); err == nil; err = windows.Process32Next(snapshot, &entry) {
			if entry.ParentProcessID != uint32(appPID) {
				continue
			}
			content := ConsoleText(entry.ProcessID)
			if strings.Contains(strings.Join(strings.Fields(content), ""), fingerprint) && strings.Contains(content, "yes/no") {
				consolePID = entry.ProcessID
				return true
			}
		}
		return false
	})
	ConsoleType(t, consolePID, "yes\r")
}

// ConsoleText reads the screen of the console that process pid uses.
func ConsoleText(pid uint32) string {
	kernel := windows.NewLazySystemDLL("kernel32.dll")
	kernel.NewProc("FreeConsole").Call()
	defer kernel.NewProc("FreeConsole").Call()
	if ok, _, _ := kernel.NewProc("AttachConsole").Call(uintptr(pid)); ok == 0 {
		return ""
	}
	name, _ := windows.UTF16PtrFromString("CONOUT$")
	handle, err := windows.CreateFile(name, windows.GENERIC_READ, windows.FILE_SHARE_READ|windows.FILE_SHARE_WRITE, nil, windows.OPEN_EXISTING, 0, 0)
	if err != nil {
		return ""
	}
	defer windows.CloseHandle(handle)
	var text [32768]uint16
	var count uint32
	kernel.NewProc("ReadConsoleOutputCharacterW").Call(uintptr(handle), uintptr(unsafe.Pointer(&text[0])), uintptr(len(text)), 0, uintptr(unsafe.Pointer(&count)))
	return syscall.UTF16ToString(text[:count])
}

// ConsoleType writes keys, such as "yes\r", as key events to the console that
// process pid uses.
func ConsoleType(t *testing.T, pid uint32, keys string) {
	t.Helper()
	kernel := windows.NewLazySystemDLL("kernel32.dll")
	kernel.NewProc("FreeConsole").Call()
	if ok, _, err := kernel.NewProc("AttachConsole").Call(uintptr(pid)); ok == 0 {
		t.Fatal("attach test console", err)
	}
	defer kernel.NewProc("FreeConsole").Call()
	name, _ := windows.UTF16PtrFromString("CONIN$")
	handle, err := windows.CreateFile(name, windows.GENERIC_WRITE, windows.FILE_SHARE_READ|windows.FILE_SHARE_WRITE, nil, windows.OPEN_EXISTING, 0, 0)
	if err != nil {
		t.Fatal(err)
	}
	defer windows.CloseHandle(handle)
	// Win32 INPUT_RECORD with KEY_EVENT_RECORD (20 bytes).
	type keyEvent struct {
		Kind, Padding               uint16
		Down                        int32
		Repeat, Virtual, Scan, Char uint16
		Control                     uint32
	}
	var input []keyEvent
	for _, char := range keys {
		key := keyEvent{Kind: 1, Down: 1, Repeat: 1, Char: uint16(char)}
		if char == '\r' {
			key.Virtual = win.VK_RETURN
		}
		input = append(input, key)
		key.Down = 0
		input = append(input, key)
	}
	var written uint32
	if ok, _, err := kernel.NewProc("WriteConsoleInputW").Call(uintptr(handle), uintptr(unsafe.Pointer(&input[0])), uintptr(len(input)), uintptr(unsafe.Pointer(&written))); ok == 0 || written != uint32(len(input)) {
		t.Fatal("write keys to test console", written, err)
	}
}

func openNativeMenu(t *testing.T, pid int) (win.HWND, win.HWND) {
	t.Helper()
	hwnd := FindNativeWindow(pid, ^uintptr(2), "GoGPUSystrayMsg")
	if hwnd == 0 {
		t.Fatal("native tray unavailable")
	}
	var popup win.HWND
	next := time.Time{}
	WaitFor(t, func() bool {
		popup = FindNativeWindow(pid, 0, "#32768")
		if popup != 0 {
			return true
		}
		// Opening a document may briefly steal focus and cancel a popup. Reopen
		// only when absent instead of waiting forever for a dismissed menu.
		if time.Now().After(next) {
			win.PostMessage(hwnd, win.WM_USER+100, 0, win.WM_RBUTTONUP)
			next = time.Now().Add(500 * time.Millisecond)
		}
		return false
	})
	return hwnd, popup
}

func NativeMenu(t *testing.T, pid int) []NativeItem {
	t.Helper()
	hwnd, popup := openNativeMenu(t, pid)
	defer func() {
		win.PostMessage(hwnd, win.WM_CANCELMODE, 0, 0)
		WaitFor(t, func() bool { return FindNativeWindow(pid, 0, "#32768") == 0 })
	}()
	menu := win.HMENU(win.SendMessage(popup, 0x01e1, 0, 0))
	var result []NativeItem
	for index := uint32(0); index < uint32(win.GetMenuItemCount(menu)); index++ {
		var label [512]uint16
		info := win.MENUITEMINFO{FMask: win.MIIM_STRING | win.MIIM_STATE | win.MIIM_FTYPE, DwTypeData: &label[0], Cch: 511}
		info.CbSize = uint32(unsafe.Sizeof(info))
		if !win.GetMenuItemInfo(menu, index, 1, &info) {
			t.Fatal("read native menu")
		}
		result = append(result, NativeItem{syscall.UTF16ToString(label[:]), info.FState&win.MFS_DISABLED != 0, info.FState&win.MFS_CHECKED != 0, info.FType&0x800 != 0})
	}
	return result
}
func NativeClick(t *testing.T, pid int, label string) {
	t.Helper()
	index := -1
	WaitFor(t, func() bool {
		for i, item := range NativeMenu(t, pid) {
			if item.Label == label && !item.Disabled {
				index = i
				return true
			}
		}
		return false
	})
	ClickTrayItem(t, pid, index)
}
func NativeError(t *testing.T, pid int) string {
	if dialog := FindNativeWindow(pid, 0, "#32770"); dialog != 0 {
		return DialogText(dialog)
	}
	return ""
}
func NativeDismiss(t *testing.T, pid int) {
	t.Helper()
	dialog := FindNativeWindow(pid, 0, "#32770")
	if dialog == 0 {
		t.Fatal("error dialog missing")
	}
	DismissErrorDialog(t, pid, dialog)
}
func windowTitles() map[win.HWND]string {
	result := map[win.HWND]string{}
	windows.NewLazySystemDLL("user32.dll").NewProc("EnumWindows").Call(syscall.NewCallback(func(hwnd win.HWND, _ uintptr) uintptr {
		if win.IsWindowVisible(hwnd) {
			var text [2048]uint16
			win.SendMessage(hwnd, win.WM_GETTEXT, uintptr(len(text)), uintptr(unsafe.Pointer(&text[0])))
			result[hwnd] = syscall.UTF16ToString(text[:])
		}
		return 1
	}), 0)
	return result
}
func NativeOpened(t *testing.T, path string) {
	t.Helper()
	name := filepath.Base(path)
	deadline := time.Now().Add(15 * time.Second)
	for time.Now().Before(deadline) {
		for hwnd, title := range windowTitles() {
			if strings.Contains(title, name) {
				if desktopWindows[hwnd] == title {
					continue // An unchanged document cannot prove another open action.
				}
				// Default editors can reuse the user's workspace. Never close it.
				if _, existed := desktopWindows[hwnd]; existed {
					desktopWindows[hwnd] = title
					return
				}
				// This document belongs to the test. Close it so a later Open
				// assertion cannot pass just because this window was left behind.
				win.PostMessage(hwnd, win.WM_CLOSE, 0, 0)
				WaitFor(t, func() bool { return !strings.Contains(windowTitles()[hwnd], name) })
				return
			}
		}
		time.Sleep(100 * time.Millisecond)
	}
	t.Fatalf("default opener did not newly display %s; close any already-open test document and use a desktop editor exposing document names in its window title. Visible windows: %v", path, windowTitles())
}
func NativeTerminals(t *testing.T) map[string]bool {
	result := map[string]bool{}
	for hwnd := range windowTitles() {
		var class [256]uint16
		win.GetClassName(hwnd, &class[0], len(class))
		name := syscall.UTF16ToString(class[:])
		if name == "ConsoleWindowClass" || name == "CASCADIA_HOSTING_WINDOW_CLASS" {
			result[strconv.FormatUint(uint64(hwnd), 10)] = true
		}
	}
	return result
}

// NativeCloseOpenedTerminal closes the console window opened since before,
// which a failed setup leaves open for the user to read.
func NativeCloseOpenedTerminal(t *testing.T, before map[string]bool) {
	t.Helper()
	for id := range NativeTerminals(t) {
		if !before[id] {
			hwnd, _ := strconv.ParseUint(id, 10, 64)
			win.PostMessage(win.HWND(hwnd), win.WM_CLOSE, 0, 0)
			return
		}
	}
	t.Fatal("the failed setup closed its terminal window")
}
func NativeTerminalsClosed(t *testing.T, before map[string]bool) {
	t.Helper()
	WaitFor(t, func() bool {
		for id := range NativeTerminals(t) {
			if !before[id] {
				return false
			}
		}
		return true
	})
}

// Invoke a real native menu item, so tests cover the app's action routing.
func ClickTrayItem(t *testing.T, pid, index int) {
	t.Helper()
	hwnd, popup := openNativeMenu(t, pid)
	menu := win.HMENU(win.SendMessage(popup, 0x01e1, 0, 0))
	info := win.MENUITEMINFO{FMask: win.MIIM_STATE}
	info.CbSize = uint32(unsafe.Sizeof(info))
	if !win.GetMenuItemInfo(menu, uint32(index), 1, &info) {
		t.Fatal("read native item state")
	}
	if info.FState&win.MFS_DISABLED != 0 {
		win.PostMessage(hwnd, win.WM_CANCELMODE, 0, 0)
	} else {
		win.SendMessage(popup, 0x01e5, uintptr(index), 0) // MN_SELECTITEM
		win.PostMessage(popup, win.WM_KEYDOWN, win.VK_RETURN, 0)
	}
	WaitFor(t, func() bool { return FindNativeWindow(pid, 0, "#32768") == 0 })
}
