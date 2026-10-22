//go:build windows || darwin

package ui

// SetStartup changes only the tray app's per-user launch-at-login preference.
func SetStartup(id, exe string, enabled bool) error {
	if enabled {
		return enableStartup(id, exe)
	}
	return disableStartup(id)
}
