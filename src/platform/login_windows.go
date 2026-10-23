package platform

import (
	"errors"
	"strings"
	"syscall"
	"unicode/utf16"

	"golang.org/x/sys/windows"
	"golang.org/x/sys/windows/registry"
)

const loginRunKey = `Software\Microsoft\Windows\CurrentVersion\Run`

func (e LoginEntry) settingsKey() string { return `Software\SSHProxy\` + e.Key }

func (e LoginEntry) commandLine() (string, bool) {
	command := windows.ComposeCommandLine(e.Command)
	if len(utf16.Encode([]rune(command))) <= 260 {
		return command, false
	}
	script := "$entry=Get-ItemProperty -LiteralPath 'HKCU:\\" + e.settingsKey() + "'; & $entry.Executable @($entry.Arguments)"
	return `powershell.exe -NoProfile -NonInteractive -WindowStyle Hidden -Command ` + syscall.EscapeArg(script), true
}

func (e LoginEntry) Enable() error {
	command, fallback := e.commandLine()
	if fallback {
		key, _, err := registry.CreateKey(registry.CURRENT_USER, e.settingsKey(), registry.SET_VALUE)
		if err != nil {
			return err
		}
		defer key.Close()
		if err := key.SetStringValue("Executable", e.Command[0]); err != nil {
			return err
		}
		if err := key.SetStringsValue("Arguments", e.Command[1:]); err != nil {
			return err
		}
	}
	key, _, err := registry.CreateKey(registry.CURRENT_USER, loginRunKey, registry.SET_VALUE)
	if err != nil {
		return err
	}
	defer key.Close()
	if err := key.SetStringValue(e.Name, command); err != nil {
		return err
	}
	if !fallback {
		return removeLoginSettings(e.settingsKey())
	}
	return nil
}

func removeLoginSettings(path string) error {
	err := registry.DeleteKey(registry.CURRENT_USER, path)
	if errors.Is(err, registry.ErrNotExist) {
		return nil
	}
	return err
}

func (e LoginEntry) Disable() error {
	key, err := registry.OpenKey(registry.CURRENT_USER, loginRunKey, registry.SET_VALUE)
	if err != nil && !errors.Is(err, registry.ErrNotExist) {
		return err
	}
	if err == nil {
		err = key.DeleteValue(e.Name)
		key.Close()
		if err != nil && !errors.Is(err, registry.ErrNotExist) {
			return err
		}
	}
	return removeLoginSettings(e.settingsKey())
}

func (e LoginEntry) Status() (enabled, stale bool, err error) {
	key, err := registry.OpenKey(registry.CURRENT_USER, loginRunKey, registry.QUERY_VALUE)
	if errors.Is(err, registry.ErrNotExist) {
		return false, false, nil
	}
	if err != nil {
		return false, false, err
	}
	defer key.Close()
	got, _, err := key.GetStringValue(e.Name)
	if errors.Is(err, registry.ErrNotExist) {
		return false, false, nil
	}
	if err != nil {
		return false, false, err
	}
	// A caller inspecting only presence need not supply the command.
	if len(e.Command) == 0 {
		return true, false, nil
	}
	want, fallback := e.commandLine()
	if got != want {
		return true, true, nil
	}
	if fallback {
		settings, err := registry.OpenKey(registry.CURRENT_USER, e.settingsKey(), registry.QUERY_VALUE)
		if errors.Is(err, registry.ErrNotExist) {
			return true, true, nil
		}
		if err != nil {
			return true, false, err
		}
		defer settings.Close()
		exe, _, err := settings.GetStringValue("Executable")
		if err != nil {
			return true, true, nil
		}
		args, _, err := settings.GetStringsValue("Arguments")
		return true, err != nil || exe != e.Command[0] || strings.Join(args, "\x00") != strings.Join(e.Command[1:], "\x00"), nil
	}
	return true, false, nil
}

// RemoveLegacyLoginSettings removes the previous app-only fallback after repair.
func RemoveLegacyLoginSettings(id string) error {
	return removeLoginSettings(`Software\SSHProxy\` + id)
}
