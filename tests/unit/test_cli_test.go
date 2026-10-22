package tests

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"ssh_proxy/src/cli"
)

// Removed or duplicate commands stay rejected before any configuration,
// OS registration or lifecycle side effect. App management belongs to the tray;
// service lifecycle uses the ordinary start, stop and restart commands.
func TestForbiddenCommandsRejectedBeforeOSAccess(t *testing.T) {
	runtime := t.TempDir()
	t.Setenv("SSH_PROXY_RUNTIME", runtime)
	configPath := filepath.Join(t.TempDir(), "config.toml")
	for _, tc := range []struct {
		args []string
		want string
	}{
		{[]string{"tray"}, "unknown command"},
		{[]string{"tray", "--hidden"}, "unknown command"},
		{[]string{"login"}, "unknown command"},
		{[]string{"startup"}, "unknown command"},
		{[]string{"startup", "enable"}, "unknown command"},
		{[]string{"startup", "disable"}, "unknown command"},
		{[]string{"startup", "status"}, "unknown command"},
		{[]string{"help"}, "unknown command"},
		{[]string{"version"}, "unknown command"},
		{[]string{"service", "start"}, "use start directly"},
		{[]string{"service", "stop"}, "use stop directly"},
		{[]string{"service", "restart"}, "use restart directly"},
	} {
		t.Run(strings.Join(tc.args, "-"), func(t *testing.T) {
			err := cli.Run(append(tc.args, "--config", configPath))
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("want %q, got %v", tc.want, err)
			}
		})
	}
	if entries, err := os.ReadDir(runtime); err != nil || len(entries) != 0 {
		t.Fatalf("rejected commands wrote runtime state: %v %v", entries, err)
	}
	if _, err := os.Stat(configPath); !os.IsNotExist(err) {
		t.Fatal("rejected commands created a configuration", err)
	}
}
