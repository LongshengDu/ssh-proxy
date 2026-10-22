package tests

import (
	"path/filepath"
	"strings"
	"testing"

	"ssh_proxy/src/proxy"
)

func TestCLIRejectsAppManagement(t *testing.T) {
	t.Setenv("SSH_PROXY_RUNTIME", t.TempDir())
	config := filepath.Join(t.TempDir(), "config.toml")
	for _, args := range [][]string{
		{"tray"}, {"tray", "--hidden"}, {"login"},
		{"startup"}, {"startup", "enable"}, {"startup", "disable"}, {"startup", "status"},
	} {
		t.Run(strings.Join(args, "-"), func(t *testing.T) {
			err := proxy.Run(append(args, "--config", config))
			if err == nil || !strings.Contains(err.Error(), "unknown command") {
				t.Fatalf("app management must not be part of the CLI: %v", err)
			}
		})
	}
}
