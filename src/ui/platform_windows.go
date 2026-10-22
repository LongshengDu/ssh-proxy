package ui

import (
	"context"
	"fmt"
	"path/filepath"
	"runtime"
	"syscall"
	"unsafe"

	"golang.org/x/sys/windows"
)

func defaultBackend(exe string) string { return filepath.Join(filepath.Dir(exe), "ssh_proxy_cli.exe") }

// Windows opens files through their registered application. If an extension
// has no association (often .toml), let the user choose through Open With.
func openPath(path string) error {
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()
	if err := windows.CoInitializeEx(0, windows.COINIT_APARTMENTTHREADED); err == nil || err == syscall.Errno(windows.S_FALSE) {
		defer windows.CoUninitialize()
	}
	file, err := windows.UTF16PtrFromString(path)
	if err != nil {
		return err
	}
	// Call ShellExecuteW directly: x/sys's wrapper drops the SE_ERR_* result code.
	shell := windows.NewLazySystemDLL("shell32.dll")
	result, _, _ := shell.NewProc("ShellExecuteW").Call(0, 0, uintptr(unsafe.Pointer(file)), 0, 0, windows.SW_SHOWNORMAL)
	if result > 32 {
		return nil
	}
	if result == 31 { // SE_ERR_NOASSOC
		info := struct {
			File, Class *uint16
			Flags       uint32
		}{File: file, Flags: 1 | 2 | 4}
		result, _, _ = shell.NewProc("SHOpenWithDialog").Call(0, uintptr(unsafe.Pointer(&info)))
		if int32(result) >= 0 {
			return nil
		}
	}
	return fmt.Errorf("Windows could not open %s (shell result %d)", path, result)
}

// Start a console program with real terminal handles, not the hidden tray's
// stdin/stdout. Windows uses the configured default terminal host. No shell.
func RunTrustTerminal(ctx context.Context, backend, logPath string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	line, err := windows.UTF16PtrFromString(windows.ComposeCommandLine([]string{backend, "trust", "--log", logPath}))
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
