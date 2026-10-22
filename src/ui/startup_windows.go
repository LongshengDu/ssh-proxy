package ui

import (
	"syscall"

	"golang.org/x/sys/windows"
	"golang.org/x/sys/windows/registry"
)

const runKey = `Software\Microsoft\Windows\CurrentVersion\Run`

func startupValue(id string) string       { return "SSHProxy-" + id }
func startupSettingsKey(id string) string { return `Software\SSHProxy\` + id }

// startupCommand starts the app directly, so Windows lists it by name.
func startupCommand(id, exe string) string {
	if command := windows.ComposeCommandLine([]string{exe}); len(command) <= 260 {
		return command
	}
	// Run values are limited to 260 characters. Store long paths as registry
	// data and pass them as arguments, never interpolate them as code.
	script := "$sshProxy=Get-ItemProperty -LiteralPath 'HKCU:\\" + startupSettingsKey(id) + "'; & $sshProxy.Executable"
	return `powershell.exe -NoProfile -NonInteractive -WindowStyle Hidden -Command ` + syscall.EscapeArg(script)
}
func enableStartup(id, exe string) error {
	settings, _, err := registry.CreateKey(registry.CURRENT_USER, startupSettingsKey(id), registry.SET_VALUE)
	if err != nil {
		return err
	}
	defer settings.Close()
	if err = settings.SetStringValue("Executable", exe); err != nil {
		return err
	}
	key, _, err := registry.CreateKey(registry.CURRENT_USER, runKey, registry.SET_VALUE)
	if err != nil {
		return err
	}
	defer key.Close()
	return key.SetStringValue(startupValue(id), startupCommand(id, exe))
}
func disableStartup(id string) error {
	key, err := registry.OpenKey(registry.CURRENT_USER, runKey, registry.SET_VALUE)
	if err != nil && err != registry.ErrNotExist {
		return err
	}
	if err == nil {
		err = key.DeleteValue(startupValue(id))
		key.Close()
		if err != nil && err != registry.ErrNotExist {
			return err
		}
	}
	err = registry.DeleteKey(registry.CURRENT_USER, startupSettingsKey(id))
	if err == registry.ErrNotExist {
		return nil
	}
	return err
}
func StartupEnabled(id string) (bool, error) {
	key, err := registry.OpenKey(registry.CURRENT_USER, runKey, registry.QUERY_VALUE)
	if err == registry.ErrNotExist {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	defer key.Close()
	_, _, err = key.GetStringValue(startupValue(id))
	if err == registry.ErrNotExist {
		return false, nil
	}
	return err == nil, err
}
