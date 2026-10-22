package ui

import (
	"context"
	"fmt"
	"path/filepath"
	"runtime"
	"syscall"
	"unsafe"

	"github.com/lxn/win"
	"golang.org/x/sys/windows"
)

func defaultBackend(exe string) string { return filepath.Join(filepath.Dir(exe), "ssh_proxy_cli.exe") }

// Windows opens files through their registered application. If an extension
// has no association (often .toml), let the user choose through Open With.
func openPath(path string) error { return shellOpen(path, "") }

func shellOpen(path, parameters string) error {
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()
	hr := win.CoInitializeEx(nil, win.COINIT_APARTMENTTHREADED)
	if hr >= 0 {
		defer win.CoUninitialize()
	}
	shell := windows.NewLazySystemDLL("shell32.dll")
	file := syscall.StringToUTF16Ptr(path)
	args := syscall.StringToUTF16Ptr(parameters)
	result, _, _ := shell.NewProc("ShellExecuteW").Call(0, 0, uintptr(unsafe.Pointer(file)), uintptr(unsafe.Pointer(args)), 0, win.SW_SHOWNORMAL)
	if result > 32 {
		return nil
	}
	if result == 31 && parameters == "" { // SE_ERR_NOASSOC
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
func RunTrustTerminal(ctx context.Context, backend, config, logPath string) error {
	line, err := windows.UTF16PtrFromString(windows.ComposeCommandLine([]string{backend, "trust", "--log", logPath, "--config", config}))
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
	if win.MessageBox(0, syscall.StringToUTF16Ptr(message), syscall.StringToUTF16Ptr("SSH Proxy — Error"), win.MB_OK|win.MB_ICONERROR|win.MB_SETFOREGROUND|win.MB_TASKMODAL) == 0 {
		return fmt.Errorf("could not show error dialog: %s", message)
	}
	return nil
}
