package config

import (
	"path/filepath"
	"runtime"

	"ssh-proxy/src/platform"
)

// AutostartEntry describes the CLI's login command, independent of the app.
func (p Paths) AutostartEntry() platform.LoginEntry {
	name, exe, args := "ssh-proxy-"+p.ID, "ssh-proxy-cli", []string{"start", "--foreground"}
	switch runtime.GOOS {
	case "windows":
		name, exe, args = "SSHProxyCLI-"+p.ID, exe+".exe", []string{"start", "--wait", "1"}
	case "darwin":
		name = "com.sshproxy.cli." + p.ID
	}
	return platform.LoginEntry{Name: name, Key: "CLI-" + p.ID, Command: append([]string{filepath.Join(filepath.Dir(p.Config), exe)}, args...)}
}
