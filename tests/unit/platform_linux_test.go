package tests

import (
	"testing"

	"ssh_proxy/src/cli"
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
