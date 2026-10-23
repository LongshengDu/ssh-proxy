//go:build systemtest && (windows || darwin)

package tests

import (
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"ssh_proxy/src/config"
	"ssh_proxy/tests/helpers"
)

// Start Proxy When App Launches (app.json) starts the proxy through the same flow as Start:
// missing hosts open ssh_config once and start nothing, an untrusted host opens
// setup at launch, a working login starts silently, and a running proxy is reused.
func TestTrayStartsProxyAtLaunch(t *testing.T) {
	testutil.NativeDesktop(t)
	dir := testutil.IsolatedBuild(t)
	f := testutil.LocalSSH(t, dir)
	// A .txt SSH config lets the test identify the editor window opened for setup.
	configPath, sshPath := filepath.Join(dir, "config.toml"), filepath.Join(dir, "servers.txt")
	text := testutil.ReadFile(filepath.Join(dir, "ssh_config"))
	hosts := text[strings.Index(text, "Host primary\n"):]
	testutil.WriteFile(t, configPath, strings.Replace(f.Config, `config_file = "ssh_config"`, `config_file = "servers.txt"`, 1))
	appPath := filepath.Join(dir, "app.json")
	if err := config.SaveAppSettings(appPath, config.AppSettings{StartProxyOnLaunch: true}); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { testutil.CallCLI(dir, "stop") })
	launch := func() (*exec.Cmd, string) {
		t.Helper()
		previous := testutil.TrayLog(dir)
		app := testutil.StartApp(t, dir)
		testutil.WaitFor(t, func() bool { return testutil.TrayLog(dir) != previous })
		return app, testutil.TrayLog(dir)
	}
	exit := func(app *exec.Cmd) {
		t.Helper()
		label := "Exit app"
		if testutil.StatusCLI(t, dir).PID != 0 {
			label = "Exit app: Keep proxy running"
		}
		testutil.NativeClick(t, app.Process.Pid, label)
		if err := app.Wait(); err != nil {
			t.Fatal(err)
		}
	}

	// No SSH server yet: only open ssh_config, without an error dialog or the log; never start SSH setup.
	testutil.WriteFile(t, sshPath, "# Add your hosts\n")
	app, log := launch()
	testutil.NativeOpened(t, sshPath)
	testutil.ExpectNoDialog(t, app.Process.Pid)
	time.Sleep(2300 * time.Millisecond)
	if strings.Contains(testutil.ReadFile(log), "Starting the proxy at app launch") || testutil.StatusCLI(t, dir).Phase != "stopped" {
		t.Fatal("invalid configuration must not start the proxy")
	}
	exit(app)

	// Untrusted host: the launch opens interactive setup, then starts.
	testutil.WriteFile(t, sshPath, hosts)
	testutil.WriteFile(t, f.Trust, "")
	before := testutil.NativeTerminals(t)
	app, log = launch()
	testutil.NativeAcceptHost(t, app.Process.Pid, before, f.Fingerprint)
	testutil.WaitFor(t, func() bool { return testutil.StatusCLI(t, dir).Phase == "running" })
	testutil.NativeTerminalsClosed(t, before)
	if text := testutil.ReadFile(log); !strings.Contains(text, "Starting the proxy at app launch") || !strings.Contains(text, "opening SSH setup terminal") {
		t.Fatal("launch did not use the setup flow", text)
	}
	testutil.ExpectMenu(t, app.Process.Pid, "Start Proxy When App Launches", false, true)
	exit(app)
	running := testutil.StatusCLI(t, dir)

	// Running proxy: a new launch reuses it without setup or a second start.
	app, log = launch()
	testutil.WaitFor(t, func() bool { return strings.Contains(testutil.ReadFile(log), "Proxy status: running") })
	if strings.Contains(testutil.ReadFile(log), "Starting the proxy at app launch") || testutil.StatusCLI(t, dir).PID != running.PID {
		t.Fatal("launch did not reuse the running proxy")
	}
	exit(app)
	testutil.MustCLI(t, dir, "stop")

	// Working login: the launch starts silently, without a terminal.
	before = testutil.NativeTerminals(t)
	app, log = launch()
	testutil.WaitFor(t, func() bool { return testutil.StatusCLI(t, dir).Phase == "running" })
	testutil.NativeTerminalsClosed(t, before)
	if strings.Contains(testutil.ReadFile(log), "opening SSH setup terminal") {
		t.Fatal("trusted login opened a setup terminal")
	}
	testutil.MustCLI(t, dir, "test", "--url", f.Origin.URL)

	// The checkbox rewrites app.json and leaves config.toml alone.
	original := testutil.ReadFile(configPath)
	testutil.NativeClick(t, app.Process.Pid, "Start Proxy When App Launches")
	testutil.WaitFor(t, func() bool { s, err := config.LoadAppSettings(appPath); return err == nil && !s.StartProxyOnLaunch })
	if testutil.ReadFile(configPath) != original {
		t.Fatal("toggling changed config.toml")
	}
	testutil.ExpectMenu(t, app.Process.Pid, "Start Proxy When App Launches", false, false)
	if testutil.StatusCLI(t, dir).Phase != "running" {
		t.Fatal("changing the setting stopped the proxy")
	}
	exit(app)
}
