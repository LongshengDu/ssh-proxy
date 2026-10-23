package platform

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"time"
)

func (e LoginEntry) Path() (string, error) {
	home, err := os.UserHomeDir()
	return filepath.Join(home, ".config", "systemd", "user", e.Name+".service"), err
}

func userSystemctl(args ...string) error {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	out, err := exec.CommandContext(ctx, "systemctl", append([]string{"--user"}, args...)...).CombinedOutput()
	if err != nil {
		return fmt.Errorf("user systemd: %w: %s", err, out)
	}
	return nil
}

func (e LoginEntry) Enable() error {
	if err := userSystemctl("show-environment"); err != nil {
		return err
	}
	path, err := e.Path()
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return err
	}
	if err := os.WriteFile(path, []byte(SystemdUnit(e.Command)), 0600); err != nil {
		return err
	}
	if err := userSystemctl("daemon-reload"); err != nil {
		return err
	}
	return userSystemctl("enable", e.Name+".service")
}

func (e LoginEntry) Disable() error {
	path, err := e.Path()
	if err != nil {
		return err
	}
	if _, err := os.Stat(path); os.IsNotExist(err) {
		return nil
	}
	if err := userSystemctl("disable", e.Name+".service"); err != nil {
		return err
	}
	if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
		return err
	}
	return userSystemctl("daemon-reload")
}

func (e LoginEntry) Status() (enabled, stale bool, err error) {
	path, err := e.Path()
	if err != nil {
		return false, false, err
	}
	link := filepath.Join(filepath.Dir(path), "default.target.wants", filepath.Base(path))
	if _, err := os.Stat(link); err != nil {
		if os.IsNotExist(err) {
			return false, false, nil
		}
		return false, false, err
	}
	data, err := os.ReadFile(path)
	return true, err == nil && len(e.Command) > 0 && string(data) != SystemdUnit(e.Command), err
}
