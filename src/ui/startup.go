//go:build windows || darwin

package ui

import (
	"runtime"
	"ssh-proxy/src/platform"
)

func appLogin(id, exe string) platform.LoginEntry {
	name := "SSHProxy-" + id
	if runtime.GOOS == "darwin" {
		name = "com.sshproxy.login." + id
	}
	entry := platform.LoginEntry{Name: name, Key: "App-" + id, AbandonProcessGroup: true}
	if exe != "" {
		entry.Command = []string{exe}
	}
	return entry
}

// SetStartup changes only the tray app's per-user launch-at-login preference.
func SetStartup(id, exe string, enabled bool) error {
	if enabled {
		if err := appLogin(id, exe).Enable(); err != nil {
			return err
		}
	} else {
		if err := appLogin(id, exe).Disable(); err != nil {
			return err
		}
	}
	return platform.RemoveLegacyLoginSettings(id)
}

func StartupEnabled(id string) (bool, error) {
	enabled, _, err := appLogin(id, "").Status()
	return enabled, err
}

func RepairStartup(id, exe string) error {
	enabled, stale, err := appLogin(id, exe).Status()
	if err != nil {
		return err
	}
	if enabled && stale {
		return SetStartup(id, exe, true)
	}
	return nil
}
