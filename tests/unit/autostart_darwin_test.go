package tests

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"ssh-proxy/src/config"
	"ssh-proxy/src/platform"
	"ssh-proxy/src/ui"
)

// The macOS counterpart of TestWindowsLoginIsolationAndRepair: outdated
// LaunchAgents are reported, the app rewrites its own at launch, and the CLI
// and app entries stay independent.
func TestMacLoginIsolationAndRepair(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	p, err := config.PathsForExecutable(filepath.Join(t.TempDir(), "ssh-proxy-cli"))
	if err != nil {
		t.Fatal(err)
	}
	cli := p.AutostartEntry()
	app := filepath.Join(filepath.Dir(p.Config), "ssh-proxy-app.app", "Contents", "MacOS", "ssh-proxy-app")
	appEntry := platform.LoginEntry{Name: "com.sshproxy.login." + p.ID}
	// Entries an older version or another folder wrote under the same labels.
	for _, old := range []platform.LoginEntry{{Name: cli.Name, Command: []string{"/old/ssh-proxy-cli", "start", "--foreground"}},
		{Name: appEntry.Name, Command: []string{"/old/ssh-proxy-app"}}} {
		if err := old.Enable(); err != nil {
			t.Fatal(err)
		}
	}
	if enabled, stale, err := cli.Status(); err != nil || !enabled || !stale {
		t.Fatal("outdated CLI entry not reported", enabled, stale, err)
	}
	if err := ui.RepairStartup(p.ID, app); err != nil {
		t.Fatal(err)
	}
	path, err := appEntry.Path()
	if err != nil {
		t.Fatal(err)
	}
	if data, err := os.ReadFile(path); err != nil || !strings.Contains(string(data), "<string>"+app+"</string>") {
		t.Fatalf("app entry not repaired: %s %v", data, err)
	}
	if err := cli.Disable(); err != nil {
		t.Fatal(err)
	}
	if enabled, err := ui.StartupEnabled(p.ID); err != nil || !enabled {
		t.Fatal("CLI disable damaged app", enabled, err)
	}
	if err := cli.Enable(); err != nil {
		t.Fatal(err)
	}
	if err := ui.SetStartup(p.ID, "", false); err != nil {
		t.Fatal(err)
	}
	if enabled, stale, err := cli.Status(); err != nil || !enabled || stale {
		t.Fatal("app disable damaged CLI", enabled, stale, err)
	}
}
