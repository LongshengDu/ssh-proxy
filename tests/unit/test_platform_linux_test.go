package tests

import (
	"path/filepath"
	"testing"

	"ssh_proxy/src/cli"
	"ssh_proxy/src/config"
)

func TestLinuxCLIWithoutSystemd(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	t.Setenv("PATH", t.TempDir()) // Neither systemctl nor a desktop session is required.
	p, err := config.PathsFor(filepath.Join(t.TempDir(), "config.toml"))
	if err != nil {
		t.Fatal(err)
	}
	for _, args := range [][]string{{"status"}, {"stop"}} {
		if err := cli.Run(append(args, "--config", p.Config)); err != nil {
			t.Fatal(args, err)
		}
	}
}
