package tests

import (
	"path/filepath"
	"strings"
	"testing"

	"golang.org/x/sys/windows/registry"
	"ssh_proxy/src/config"
	"ssh_proxy/src/platform"
	"ssh_proxy/src/ui"
)

func TestWindowsLoginIsolationAndRepair(t *testing.T) {
	p, err := config.PathsForExecutable(filepath.Join(t.TempDir(), "ssh_proxy_cli.exe"))
	if err != nil {
		t.Fatal(err)
	}
	cli := p.AutostartEntry()
	app := filepath.Join(filepath.Dir(p.Config), "ssh_proxy_app.exe")
	t.Cleanup(func() { cli.Disable(); ui.SetStartup(p.ID, "", false) })
	key, _, err := registry.CreateKey(registry.CURRENT_USER, `Software\Microsoft\Windows\CurrentVersion\Run`, registry.ALL_ACCESS)
	if err != nil {
		t.Fatal(err)
	}
	defer key.Close()
	if err := key.SetStringValue("SSHProxy-"+p.ID, "powershell.exe obsolete --config"); err != nil {
		t.Fatal(err)
	}
	if err := ui.RepairStartup(p.ID, app); err != nil {
		t.Fatal(err)
	}
	got, _, err := key.GetStringValue("SSHProxy-" + p.ID)
	if err != nil || strings.Contains(got, "powershell") || !strings.Contains(got, "ssh_proxy_app.exe") {
		t.Fatal(got, err)
	}
	if _, err := registry.OpenKey(registry.CURRENT_USER, `Software\SSHProxy\App-`+p.ID, registry.READ); err != registry.ErrNotExist {
		t.Fatal("short path must not create fallback settings", err)
	}
	// Both long paths need their own executable and argument data.
	cli.Command[0] = `C:\` + strings.Repeat("long path\\", 30) + "ssh_proxy_cli.exe"
	longApp := strings.Replace(cli.Command[0], "cli.exe", "app.exe", 1)
	if err := cli.Enable(); err != nil {
		t.Fatal(err)
	}
	if err := ui.SetStartup(p.ID, longApp, true); err != nil {
		t.Fatal(err)
	}
	if enabled, stale, err := cli.Status(); err != nil || !enabled || stale {
		t.Fatal(enabled, stale, err)
	}
	if err := ui.SetStartup(p.ID, "", false); err != nil {
		t.Fatal(err)
	}
	if enabled, stale, err := cli.Status(); err != nil || !enabled || stale {
		t.Fatal("app disable damaged CLI", enabled, stale, err)
	}
	if err := ui.SetStartup(p.ID, longApp, true); err != nil {
		t.Fatal(err)
	}
	if err := cli.Disable(); err != nil {
		t.Fatal(err)
	}
	if enabled, _, err := (platform.LoginEntry{Name: "SSHProxy-" + p.ID}).Status(); err != nil || !enabled {
		t.Fatal("CLI disable damaged app", enabled, err)
	}
}
