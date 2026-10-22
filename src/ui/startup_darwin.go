package ui

import (
	"encoding/xml"
	"os"
	"path/filepath"
	"strings"
)

func startupPath(id string) (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, "Library", "LaunchAgents", "com.sshproxy.login."+id+".plist"), nil
}
func enableStartup(id, exe, config string) error {
	path, err := startupPath(id)
	if err != nil {
		return err
	}
	if err = os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return err
	}
	esc := func(value string) string { var b strings.Builder; xml.EscapeText(&b, []byte(value)); return b.String() }
	data := `<?xml version="1.0" encoding="UTF-8"?><!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd"><plist version="1.0"><dict><key>Label</key><string>com.sshproxy.login.` + id + `</string><key>ProgramArguments</key><array><string>` + esc(exe) + `</string><string>--config</string><string>` + esc(config) + `</string></array><key>RunAtLoad</key><true/><key>AbandonProcessGroup</key><true/></dict></plist>`
	return os.WriteFile(path, []byte(data), 0600)
}
func disableStartup(id string) error {
	path, err := startupPath(id)
	if err != nil {
		return err
	}
	err = os.Remove(path)
	if os.IsNotExist(err) {
		return nil
	}
	return err
}
func StartupEnabled(id string) (bool, error) {
	path, err := startupPath(id)
	if err != nil {
		return false, err
	}
	_, err = os.Stat(path)
	if os.IsNotExist(err) {
		return false, nil
	}
	return err == nil, err
}
