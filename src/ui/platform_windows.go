package ui

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"syscall"
	"unsafe"

	"golang.org/x/sys/windows"
)

func defaultBackend(exe string) string { return filepath.Join(filepath.Dir(exe), "ssh_proxy_cli.exe") }

// trayIconSize is the small-icon size the notification area draws. The app's
// manifest declares DPI awareness, so this follows the display scaling.
func trayIconSize() int {
	size, _, _ := windows.NewLazySystemDLL("user32.dll").NewProc("GetSystemMetrics").Call(49) // SM_CXSMICON
	if size == 0 {
		return 16
	}
	return int(size)
}

// Configs and logs are text, regardless of their filename. Use the .txt editor
// and fall back to Notepad instead of displaying an application picker.
func openPath(path string) error {
	info, err := os.Stat(path)
	if err != nil {
		return err
	}
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()
	if err := windows.CoInitializeEx(0, windows.COINIT_APARTMENTTHREADED); err == nil || err == syscall.Errno(windows.S_FALSE) {
		defer windows.CoUninitialize()
	}
	file, err := windows.UTF16PtrFromString(path)
	if err != nil {
		return err
	}
	if info.IsDir() {
		return windows.ShellExecute(0, nil, file, nil, nil, windows.SW_SHOWNORMAL)
	}
	class, _ := windows.UTF16PtrFromString(".txt")
	// SHELLEXECUTEINFOW; SEE_MASK_CLASSNAME overrides the file's association.
	request := struct {
		Size, Mask                  uint32
		Window                      uintptr
		Verb, File, Parameters, Dir *uint16
		Show                        int32
		Instance, IDList            uintptr
		Class                       *uint16
		ClassKey                    uintptr
		HotKey                      uint32
		Icon, Process               uintptr
	}{Mask: 0x1 | 0x100 | 0x400, File: file, Class: class, Show: windows.SW_SHOWNORMAL}
	// NOASYNC waits for shell dispatch; FLAG_NO_UI prevents an app picker/error UI.
	request.Size = uint32(unsafe.Sizeof(request))
	result, _, _ := windows.NewLazySystemDLL("shell32.dll").NewProc("ShellExecuteExW").Call(uintptr(unsafe.Pointer(&request)))
	if result != 0 {
		return nil
	}
	system, err := windows.GetSystemDirectory()
	if err != nil {
		return err
	}
	program, _ := windows.UTF16PtrFromString(filepath.Join(system, "notepad.exe"))
	args, _ := windows.UTF16PtrFromString(windows.ComposeCommandLine([]string{path}))
	return windows.ShellExecute(0, nil, program, args, nil, windows.SW_SHOWNORMAL)
}

// Start a console program with real terminal handles, not the hidden tray's
// stdin/stdout. Windows uses the configured default terminal host. No shell.
func RunTrustTerminal(ctx context.Context, backend, logPath string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	line, err := windows.UTF16PtrFromString(windows.ComposeCommandLine([]string{backend, "login", "--log", logPath}))
	if err != nil {
		return err
	}
	startup := windows.StartupInfo{}
	startup.Cb = uint32(unsafe.Sizeof(startup))
	var process windows.ProcessInformation
	if err = windows.CreateProcess(nil, line, nil, nil, false, windows.CREATE_NEW_CONSOLE, nil, nil, &startup, &process); err != nil {
		return err
	}
	defer windows.CloseHandle(process.Process)
	windows.CloseHandle(process.Thread)
	for ctx.Err() == nil {
		state, err := windows.WaitForSingleObject(process.Process, 200)
		if err != nil {
			return err
		}
		if state == windows.WAIT_OBJECT_0 {
			var code uint32
			if err := windows.GetExitCodeProcess(process.Process, &code); err != nil {
				return err
			}
			if code != 0 {
				return fmt.Errorf("SSH setup exited with code %d", code)
			}
			return nil
		}
	}
	return ctx.Err() // Closing the tray leaves the user's setup terminal alone.
}

func errorMessage(message string) error {
	text, err := windows.UTF16PtrFromString(message)
	if err != nil {
		return err
	}
	title, _ := windows.UTF16PtrFromString("SSH Proxy — Error")
	if ret, err := windows.MessageBox(0, text, title, windows.MB_OK|windows.MB_ICONERROR|windows.MB_SETFOREGROUND|windows.MB_TASKMODAL); ret == 0 {
		return fmt.Errorf("could not show error dialog: %w: %s", err, message)
	}
	return nil
}
