package platform

import (
	"os"
	"path/filepath"
)

func (e LoginEntry) Path() (string, error) {
	home, err := os.UserHomeDir()
	return filepath.Join(home, "Library", "LaunchAgents", e.Name+".plist"), err
}

func (e LoginEntry) Enable() error {
	path, err := e.Path()
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return err
	}
	// Login loads the entry. Do not bootstrap/kickstart here: enable never starts.
	return os.WriteFile(path, []byte(e.LaunchAgent()), 0600)
}

func (e LoginEntry) Disable() error {
	path, err := e.Path()
	if err != nil {
		return err
	}
	// bootout would terminate the current foreground proxy.
	err = os.Remove(path)
	if os.IsNotExist(err) {
		return nil
	}
	return err
}

func (e LoginEntry) Status() (enabled, stale bool, err error) {
	path, err := e.Path()
	if err != nil {
		return false, false, err
	}
	data, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return false, false, nil
	}
	return err == nil, err == nil && len(e.Command) > 0 && string(data) != e.LaunchAgent(), err
}

func RemoveLegacyLoginSettings(string) error { return nil }
