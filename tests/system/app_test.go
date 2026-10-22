//go:build systemtest && (windows || darwin)

package tests

import (
	"encoding/json"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"testing"
	"time"

	"ssh_proxy/src/config"
	"ssh_proxy/src/control"
	"ssh_proxy/src/platform"
	"ssh_proxy/src/ui"
	"ssh_proxy/tests/helpers"
)

// The same workflow runs against the actual app + CLI on Windows and macOS.
func TestTrayNativeWorkflow(t *testing.T) {
	testutil.NativeDesktop(t)
	dir := testutil.IsolatedBuild(t)
	f := testutil.LocalSSH(t, dir)
	suffix := strconv.FormatInt(time.Now().UnixNano(), 36)
	configPath := filepath.Join(dir, "config.toml")
	servers := filepath.Join(dir, "servers-"+suffix+".txt")
	testutil.WriteFile(t, configPath, strings.Replace(f.Config, `config_file = "ssh_config"`, `config_file = "`+filepath.Base(servers)+`"`, 1))
	sshText := testutil.ReadFile(filepath.Join(dir, "ssh_config"))
	// Ordered fallback is exercised by CLI tests. This UI workflow starts with
	// healthy endpoints so transient connection-error dialogs cannot mask later assertions.
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
	testutil.ExpectDialog(t, pid, "add your hosts")
	testutil.NativeOpened(t, servers) // Prove the default file opener displayed the failing config.
	testutil.NativeDismiss(t, pid)
	log := testutil.TrayLog(dir)
	testutil.NativeOpened(t, log)
	testutil.WriteFile(t, servers, sshText)
	testutil.WaitFor(t, func() bool { return strings.Contains(testutil.ReadFile(log), "Proxy status: stopped") })
	testutil.ExpectMenu(t, pid, "Start proxy", false, false)
	testutil.ExpectMenu(t, pid, "Run: Fixture", true, false)
	for _, enabled := range []bool{true, false} {
		testutil.NativeClick(t, pid, "Launch App at Login")
		testutil.WaitFor(t, func() bool { got, err := ui.StartupEnabled(p.ID); return err == nil && got == enabled })
		time.Sleep(250 * time.Millisecond)
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
	testutil.ExpectMenu(t, pid, "Stop proxy", false, false)
	st := status()
	testutil.ExpectMenu(t, pid, "SSH: "+st.Destination+" ("+st.Endpoint+")", true, false)
	testutil.ExpectMenu(t, pid, "SOCKS5: "+st.SOCKS, true, false)
	testutil.ExpectMenu(t, pid, "HTTP: "+st.HTTP, true, false)
	call("test", "--url", f.Origin.URL)
	f.Blocked.Store(true)
	testutil.ExpectDialog(t, pid, "all forwarding probes failed")
	f.Blocked.Store(false)
	testutil.NativeDismiss(t, pid)
	testutil.NativeOpened(t, log)
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
	time.Sleep(2300 * time.Millisecond)
	testutil.ExpectMenu(t, pid, "SSH: —", true, false)
	testutil.ExpectMenu(t, pid, "HTTP: —", true, false)
	testutil.ExpectMenu(t, pid, "Start proxy", false, false)
	// Starting from the CLI and exiting the tray must leave the same tunnel alive.
	call("start", "--wait", "15")
	testutil.WaitFor(t, func() bool { return strings.Count(testutil.ReadFile(log), "Proxy status: running") >= 2 })
	active := status()
	testutil.NativeClick(t, pid, "Keep proxy running and exit")
	if err := app.Wait(); err != nil {
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
	testutil.NativeClick(t, app.Process.Pid, "Stop proxy and exit")
	testutil.WaitFor(t, func() bool { return !platform.ProcessAlive(app.Process.Pid) })
	if err := app.Wait(); err != nil {
		t.Fatal(err)
	}
	if status().PID != 0 || platform.ProcessAlive(active.PID) || platform.ProcessAlive(active.SSHPID) {
		t.Fatal("stop-and-exit left the proxy or SSH child running")
	}
}
