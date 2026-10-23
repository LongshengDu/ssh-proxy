package tests

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"ssh-proxy/src/cli"
	"ssh-proxy/src/config"
	"ssh-proxy/src/platform"
)

func TestLinuxCLIWithoutSystemd(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	t.Setenv("PATH", t.TempDir()) // Neither systemctl nor a desktop session is required.
	t.Setenv("SSH_PROXY_RUNTIME", t.TempDir())
	for _, args := range [][]string{{"status"}, {"stop"}} {
		if err := cli.Run(args); err != nil {
			t.Fatal(args, err)
		}
	}
}

// A unit for another command, from an older version or a moved folder, is
// reported as outdated, as TestWindowsLoginIsolationAndRepair checks for Windows.
func TestLinuxOutdatedAutostart(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	p, err := config.PathsForExecutable(filepath.Join(t.TempDir(), "ssh-proxy-cli"))
	if err != nil {
		t.Fatal(err)
	}
	entry := p.AutostartEntry()
	path, err := entry.Path()
	if err != nil {
		t.Fatal(err)
	}
	// What systemctl enable creates; the test needs no systemd.
	wants := filepath.Join(filepath.Dir(path), "default.target.wants")
	if err := os.MkdirAll(wants, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(path, filepath.Join(wants, filepath.Base(path))); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		command []string
		stale   bool
	}{{[]string{"/old/ssh-proxy-cli", "start", "--foreground"}, true}, {entry.Command, false}} {
		if err := os.WriteFile(path, []byte(platform.SystemdUnit(tc.command)), 0600); err != nil {
			t.Fatal(err)
		}
		if enabled, stale, err := entry.Status(); err != nil || !enabled || stale != tc.stale {
			t.Fatal(tc.command, enabled, stale, err)
		}
	}
}

// Autostart runs under the systemd user manager, which may not have the login
// shell's XDG_CACHE_HOME, so runtime state and locks must not depend on it.
func TestLinuxStateIgnoresXDGCacheHome(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	t.Setenv("SSH_PROXY_RUNTIME", "")
	p, err := config.PathsForExecutable(filepath.Join(t.TempDir(), "ssh-proxy-cli"))
	if err != nil || !strings.HasPrefix(p.Runtime, filepath.Join(home, ".cache", "ssh-proxy")+string(os.PathSeparator)) {
		t.Fatal("runtime state must live in ~/.cache:", p.Runtime, err)
	}
}
