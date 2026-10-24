//go:build systemtest && (windows || darwin)

package tests

import (
	"encoding/json"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"ssh-proxy/src/config"
	"ssh-proxy/src/control"
	"ssh-proxy/src/platform"
	"ssh-proxy/src/ui"
	"ssh-proxy/tests/helpers"
)

// The same workflow runs against the actual app + CLI on Windows and macOS.
func TestTrayNativeWorkflow(t *testing.T) {
	testutil.NativeDesktop(t)
	dir := testutil.IsolatedBuild(t)
	f := testutil.LocalSSH(t, dir)
	configPath := filepath.Join(dir, "config.toml")
	// Exercise the shipped filename: its lack of an extension affects the OS opener.
	servers := filepath.Join(dir, "ssh_config")
	sshText := testutil.ReadFile(servers)
	// Ordered fallback is exercised by CLI tests. This UI workflow starts with
	// healthy endpoints.
	sshText = sshText[strings.Index(sshText, "Host primary\n"):]
	testutil.WriteFile(t, servers, "# Add your hosts\n")
	p, err := config.PathsForExecutable(testutil.CLIPath(dir))
	if err != nil {
		t.Fatal(err)
	}
	call := func(args ...string) []byte { return testutil.MustCLI(t, dir, args...) }
	status := func() control.Status {
		var st control.Status
		if err := json.Unmarshal(call("status"), &st); err != nil {
			t.Fatal(err)
		}
		return st
	}
	t.Cleanup(func() { testutil.CallCLI(dir, "stop"); ui.SetStartup(p.ID, "", false) })
	script := filepath.Join(dir, "scripts", "Fixture.sh")
	record := filepath.Join(dir, "script-ran")
	scriptText := "printf 'ok' > '" + filepath.ToSlash(record) + "'\n"
	if runtime.GOOS == "windows" {
		script = filepath.Join(dir, "scripts", "Fixture.ps1")
		scriptText = "$ErrorActionPreference = 'Stop'\nSet-Content -LiteralPath (Join-Path (Split-Path -Parent $PSScriptRoot) 'script-ran') -Value 'ok'"
	}
	testutil.WriteFile(t, script, scriptText)
	app := testutil.StartApp(t, dir)
	pid := app.Process.Pid
	// First launch without a server: the shipped ssh_config opens by itself, with no error dialog or log.
	testutil.NativeOpened(t, servers)
	testutil.ExpectNoDialog(t, pid)
	log := testutil.TrayLog(dir)
	testutil.WriteFile(t, servers, sshText)
	testutil.WaitFor(t, func() bool { return strings.Contains(testutil.ReadFile(log), "Proxy status: stopped") })
	scripts := []string{"Claude", "Codex", "Fixture"} // The shipped launchers and this test's script.
	testutil.ExpectMenuLayout(t, pid, scripts, nil, false)
	for _, enabled := range []bool{true, false} {
		testutil.NativeClick(t, pid, "Launch App at Login")
		testutil.WaitFor(t, func() bool { got, err := ui.StartupEnabled(p.ID); return err == nil && got == enabled })
		testutil.ExpectMenu(t, pid, "Launch App at Login", false, enabled)
	}
	for _, item := range []struct{ label, path string }{{"Open SSH Config", servers}, {"Open App Config", configPath}, {"Open Current Log", log}, {"Open Installed Location", dir}} {
		testutil.NativeClick(t, pid, item.label)
		testutil.NativeOpened(t, item.path)
	}
	before := testutil.NativeTerminals(t)
	testutil.NativeClick(t, pid, "Start proxy")
	testutil.WaitFor(t, func() bool { return status().Phase == "running" })
	testutil.WaitFor(t, func() bool { return strings.Contains(testutil.ReadFile(log), "Proxy status: running") })
	testutil.NativeTerminalsClosed(t, before)
	if strings.Contains(testutil.ReadFile(log), "opening SSH setup terminal") {
		t.Fatal("a working unattended login must start without a setup terminal")
	}
	st := status()
	testutil.ExpectMenuLayout(t, pid, scripts, &st, false)
	call("test", "--url", f.Origin.URL)
	// Health failures while the proxy retries go to the tooltip and log, never a dialog.
	f.Blocked.Store(true)
	testutil.WaitFor(t, func() bool {
		return strings.Contains(testutil.ReadFile(log), "Proxy error: all forwarding probes failed")
	})
	testutil.ExpectNoDialog(t, pid)
	f.Blocked.Store(false)
	testutil.WaitFor(t, func() bool { return status().Phase == "running" })
	testutil.NativeClick(t, pid, "Run: Fixture")
	testutil.WaitFor(t, func() bool { return strings.TrimSpace(testutil.ReadFile(record)) == "ok" })
	// Errors append to the same app log and open that file, never a new error log.
	if runtime.GOOS == "windows" {
		testutil.WriteFile(t, script, "throw 'fixture script failure'")
	} else {
		testutil.WriteFile(t, script, "echo 'fixture script failure' >&2; exit 7\n")
	}
	time.Sleep(300 * time.Millisecond)
	testutil.NativeClick(t, pid, "Run: Fixture")
	message := testutil.ExpectDialog(t, pid, "Run: Fixture")
	if !strings.Contains(message, log) || !strings.Contains(testutil.ReadFile(log), "fixture script failure") {
		t.Fatal("error/log mismatch", message)
	}
	testutil.NativeDismiss(t, pid)
	testutil.NativeOpened(t, log)
	testutil.NativeClick(t, pid, "Stop proxy")
	testutil.WaitFor(t, func() bool { return status().Phase == "stopped" })
	testutil.WaitFor(t, func() bool { return strings.Contains(testutil.ReadFile(log), "Proxy status: stopped") })
	testutil.ExpectMenuLayout(t, pid, scripts, nil, false) // No stale host or addresses after stop.
	// Starting from the CLI and exiting the tray must leave the same tunnel alive.
	call("start", "--wait", "15")
	testutil.WaitFor(t, func() bool { return strings.Count(testutil.ReadFile(log), "Proxy status: running") >= 2 })
	active := status()
	testutil.NativeClick(t, pid, "Exit app: Keep proxy running")
	if err := testutil.WaitExit(t, app); err != nil {
		t.Fatal(err)
	}
	if status().PID != active.PID {
		t.Fatal("tray exit stopped proxy")
	}
	call("test", "--url", f.Origin.URL)
	files, _ := filepath.Glob(filepath.Join(dir, "logs", "tray_*.txt"))
	if len(files) != 1 || !strings.Contains(testutil.ReadFile(log), "Tray exited;") {
		t.Fatal("app run log split or missing shutdown", files)
	}
	// The other exit choice must stop the same supervisor and SSH child first.
	app = testutil.StartApp(t, dir)
	testutil.WaitFor(t, func() bool {
		currentLog := testutil.TrayLog(dir)
		return currentLog != log && strings.Contains(testutil.ReadFile(currentLog), "Proxy status: running")
	})
	testutil.NativeClick(t, app.Process.Pid, "Exit app: Stop proxy")
	testutil.WaitFor(t, func() bool { return !platform.ProcessAlive(app.Process.Pid) })
	if err := testutil.WaitExit(t, app); err != nil {
		t.Fatal(err)
	}
	if status().PID != 0 || platform.ProcessAlive(active.PID) || platform.ProcessAlive(active.SSHPID) {
		t.Fatal("stop-and-exit left the proxy or SSH child running")
	}
}
