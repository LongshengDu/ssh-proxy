package tests

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"ssh-proxy/src/cli"
	"ssh-proxy/src/config"
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
