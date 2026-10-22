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

// app.start_proxy_on_launch starts the proxy through the same flow as Start:
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
	// Keep the public default's bilingual [app] comments: the tray edits only its own line.
	template := string(testutil.ConfigTemplate(t))
	settings := strings.Replace(f.Config, `config_file = "ssh_config"`, `config_file = "servers.txt"`, 1)
	testutil.WriteFile(t, configPath, settings+"\n"+template[strings.Index(template, "# 仅 Windows/macOS"):])
	if err := config.SetStartProxyOnLaunch(configPath, true); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { testutil.CallCLI(dir, "stop") })
	launch := func() (*exec.Cmd, string) {
		t.Helper()
		previous := testutil.TrayLog(dir)
		app := testutil.StartApp(t, dir, configPath, testutil.CLIPath(dir))
		testutil.WaitFor(t, func() bool { return testutil.TrayLog(dir) != previous })
		return app, testutil.TrayLog(dir)
	}
	exit := func(app *exec.Cmd) {
		t.Helper()
		label := "Exit app"
		if testutil.StatusCLI(t, dir).PID != 0 {
			label = "Keep proxy running and exit"
		}
		testutil.NativeClick(t, app.Process.Pid, label)
		if err := app.Wait(); err != nil {
			t.Fatal(err)
		}
	}

	// Missing hosts: report and open ssh_config once; never start SSH setup.
	testutil.WriteFile(t, sshPath, "# Add your hosts\n")
	app, log := launch()
	testutil.ExpectDialog(t, app.Process.Pid, "add your hosts")
	testutil.NativeOpened(t, sshPath)
	testutil.NativeDismiss(t, app.Process.Pid)
	testutil.NativeOpened(t, log)
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

	// The checkbox edits only its own line of config.toml.
	original := testutil.ReadFile(configPath)
	testutil.NativeClick(t, app.Process.Pid, "Start Proxy When App Launches")
	testutil.WaitFor(t, func() bool { enabled, err := config.StartProxyOnLaunch(configPath); return err == nil && !enabled })
	if want := strings.Replace(original, "start_proxy_on_launch = true", "start_proxy_on_launch = false", 1); testutil.ReadFile(configPath) != want {
		t.Fatal("toggling changed other lines of config.toml")
	}
	testutil.ExpectMenu(t, app.Process.Pid, "Start Proxy When App Launches", false, false)
	if testutil.StatusCLI(t, dir).Phase != "running" {
		t.Fatal("changing the setting stopped the proxy")
	}
	exit(app)
}
