//go:build systemtest && (windows || darwin)

package tests

import (
	"encoding/json"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"

	"ssh_proxy/src/config"
	"ssh_proxy/src/platform"
	"ssh_proxy/src/ui"
	"ssh_proxy/tests/helpers"
)

func TestTrayLoginLaunch(t *testing.T) {
	testutil.NativeDesktop(t)
	dir := testutil.IsolatedBuild(t)
	testutil.LocalSSH(t, dir)
	configPath := filepath.Join(dir, "config.toml")
	testutil.WriteFile(t, configPath, testutil.ReadFile(configPath)+"\n[logging]\nmax_log_files = 1\n")
	testutil.SeedRunLogs(t, filepath.Join(dir, "logs"), "tray", 3)
	proxyLogs := testutil.SeedRunLogs(t, filepath.Join(dir, "logs"), "logs", 3)
	p, err := config.PathsForExecutable(configPath, testutil.CLIPath(dir))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { ui.SetStartup(p.ID, "", "", false) })
	app := testutil.StartApp(t, dir, configPath, testutil.CLIPath(dir))
	testutil.WaitFor(t, func() bool {
		return strings.Contains(testutil.ReadFile(testutil.TrayLog(dir)), "Proxy status: stopped")
	})
	testutil.NativeClick(t, app.Process.Pid, "Launch App at Login")
	testutil.WaitFor(t, func() bool { enabled, err := ui.StartupEnabled(p.ID); return err == nil && enabled })
	testutil.NativeClick(t, app.Process.Pid, "Exit app (proxy keeps running)")
	if err := app.Wait(); err != nil {
		t.Fatal(err)
	}
	oldLog := testutil.TrayLog(dir)
	testutil.NativeLoginLaunch(t, p.ID, testutil.AppPath(dir), configPath)
	testutil.WaitFor(t, func() bool {
		return testutil.TrayLog(dir) != oldLog && strings.Contains(testutil.ReadFile(testutil.TrayLog(dir)), "Proxy status: stopped")
	})
	match := regexp.MustCompile(`Tray started; pid=(\d+)`).FindStringSubmatch(testutil.ReadFile(testutil.TrayLog(dir)))
	if len(match) != 2 {
		t.Fatal("login app did not identify its PID")
	}
	pid, _ := strconv.Atoi(match[1])
	t.Cleanup(func() {
		if process, err := os.FindProcess(pid); err == nil {
			process.Kill()
		}
	})
	logs, err := filepath.Glob(filepath.Join(dir, "logs", "tray_*.txt"))
	if err != nil || len(logs) != 1 || logs[0] == oldLog {
		t.Fatal("tray startup must keep only its current log with limit 1", logs, err)
	}
	for _, path := range proxyLogs {
		if _, err := os.Stat(path); err != nil {
			t.Fatal("tray startup removed a proxy log", err)
		}
	}
	if testutil.StatusCLI(t, dir).Phase != "stopped" {
		t.Fatal("login launch started the proxy")
	}
	testutil.NativeClick(t, pid, "Exit app (proxy keeps running)")
	testutil.WaitFor(t, func() bool { return strings.Contains(testutil.ReadFile(testutil.TrayLog(dir)), "Tray exited;") })
}

// Every Start first checks unattended login silently. Only a failed check
// opens the interactive setup terminal, and a successful setup is checked again.
func TestTrayInteractiveTerminal(t *testing.T) {
	testutil.NativeDesktop(t)
	for _, tc := range []struct{ name, trustExit, batch, dialog string }{
		{"already-trusted", "0", "always", ""},
		{"setup-ok", "0", "", ""},
		{"setup-failed", "7", "", "code 7"},
		{"password-only", "0", "never", "unattended login still fails"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dir := testutil.IsolatedBuild(t)
			helper, err := os.Executable()
			if err != nil {
				t.Fatal(err)
			}
			data, err := os.ReadFile(helper)
			if err != nil {
				t.Fatal(err)
			}
			backend := testutil.CLIPath(dir)
			if err := os.WriteFile(backend, data, 0755); err != nil {
				t.Fatal(err)
			}
			configPath, record, gate := filepath.Join(dir, "literal ' config.txt"), filepath.Join(dir, "commands.jsonl"), filepath.Join(dir, "release-trust")
			settings := map[string]string{"SSH_PROXY_TEST_PROCESS": "cli", "SSH_PROXY_TEST_RECORD": record, "SSH_PROXY_TEST_GATE": gate,
				"SSH_PROXY_TEST_TRUST_EXIT": tc.trustExit, "SSH_PROXY_TEST_BATCH": tc.batch}
			data, _ = json.Marshal(settings)
			testutil.WriteFile(t, filepath.Join(dir, "fixture.json"), string(data))
			t.Cleanup(func() { os.WriteFile(gate, nil, 0600) })
			type command struct {
				Args                     []string
				Terminal, OutputTerminal bool
				PID                      int
			}
			commands := func() (silent int, setup *command, started bool) {
				for _, line := range strings.Split(testutil.ReadFile(record), "\n") {
					var entry command
					if json.Unmarshal([]byte(line), &entry) != nil || len(entry.Args) == 0 {
						continue
					}
					switch {
					case entry.Args[0] == "start":
						started = true
					case slices.Contains(entry.Args, "--batch"):
						silent++
						if entry.Terminal {
							t.Fatalf("silent login check had a terminal: %+v", entry)
						}
					case entry.Args[0] == "trust" && setup == nil:
						setup = &entry
					}
				}
				return
			}
			before := testutil.NativeTerminals(t)
			app := testutil.StartApp(t, dir, configPath, backend)
			pid := app.Process.Pid
			testutil.WaitFor(t, func() bool {
				return strings.Contains(testutil.ReadFile(testutil.TrayLog(dir)), "Proxy status: stopped")
			})
			testutil.NativeClick(t, pid, "Start proxy")
			if tc.batch == "always" {
				testutil.WaitFor(t, func() bool { _, _, started := commands(); return started })
				if silent, setup, _ := commands(); silent != 1 || setup != nil {
					t.Fatalf("working unattended login must start without a terminal: %d checks, setup %+v", silent, setup)
				}
				testutil.NativeTerminalsClosed(t, before)
			} else {
				var entry *command
				testutil.WaitFor(t, func() bool { _, entry, _ = commands(); return entry != nil })
				if !entry.Terminal || !entry.OutputTerminal || len(entry.Args) != 5 || entry.Args[0] != "trust" || entry.Args[4] != configPath {
					t.Fatalf("trust lost terminal handles or literal paths: %+v", entry)
				}
				if !platform.ProcessAlive(entry.PID) {
					t.Fatal("setup exited before input was supplied")
				}
				time.Sleep(2300 * time.Millisecond)
				if _, _, started := commands(); started {
					t.Fatal("start ran before trust completed")
				}
				testutil.ExpectMenu(t, pid, "Start proxy", true, false)
				testutil.WriteFile(t, gate, "")
				if tc.dialog == "" {
					testutil.WaitFor(t, func() bool { _, _, started := commands(); return started })
					if silent, _, _ := commands(); silent != 2 {
						t.Fatalf("setup must be followed by a second silent check: %d", silent)
					}
				} else {
					testutil.ExpectDialog(t, pid, tc.dialog)
					if _, _, started := commands(); started {
						t.Fatal("failed setup started the proxy")
					}
					testutil.NativeDismiss(t, pid)
				}
				testutil.WaitFor(t, func() bool { return !platform.ProcessAlive(entry.PID) })
				testutil.NativeTerminalsClosed(t, before)
			}
			testutil.NativeClick(t, pid, "Exit app (proxy keeps running)")
			if err := app.Wait(); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestTrayRecoveryErrors(t *testing.T) {
	testutil.NativeDesktop(t)
	for _, scenario := range []string{"bad config", "missing CLI", "empty SSH", "missing SSH"} {
		t.Run(scenario, func(t *testing.T) {
			dir := testutil.IsolatedBuild(t)
			suffix := strconv.FormatInt(time.Now().UnixNano(), 36)
			configPath, servers := filepath.Join(dir, "settings-"+suffix+".txt"), filepath.Join(dir, "servers-"+suffix+".txt")
			text := strings.ReplaceAll(string(testutil.ConfigTemplate(t)), `config_file = "ssh_config"`, `config_file = "`+filepath.Base(servers)+`"`)
			testutil.WriteFile(t, configPath, text)
			reason := "SSH config file not found"
			switch scenario {
			case "bad config":
				testutil.WriteFile(t, configPath, "proxy = [")
				reason = "toml"
			case "missing CLI":
				if err := os.Remove(testutil.CLIPath(dir)); err != nil {
					t.Fatal(err)
				}
				reason = "backend missing"
			case "empty SSH":
				testutil.WriteFile(t, servers, "# hosts\n")
				reason = "add your hosts"
			}
			app := testutil.StartApp(t, dir, configPath, testutil.CLIPath(dir))
			pid := app.Process.Pid
			message := testutil.ExpectDialog(t, pid, reason)
			log := testutil.TrayLog(dir)
			if !strings.Contains(message, log) || !strings.Contains(testutil.ReadFile(log), reason) {
				t.Fatal("error was not recorded in the app run log", message)
			}
			if strings.HasSuffix(scenario, "SSH") {
				testutil.NativeOpened(t, servers)
			}
			testutil.NativeDismiss(t, pid)
			testutil.NativeOpened(t, log)
			if scenario != "missing CLI" {
				if scenario == "bad config" {
					p, err := config.PathsForExecutable(configPath, testutil.CLIPath(dir))
					if err != nil {
						t.Fatal(err)
					}
					t.Cleanup(func() { ui.SetStartup(p.ID, "", "", false) })
					for _, enabled := range []bool{true, false} {
						testutil.NativeClick(t, pid, "Launch App at Login")
						testutil.WaitFor(t, func() bool { got, err := ui.StartupEnabled(p.ID); return err == nil && got == enabled })
						time.Sleep(300 * time.Millisecond)
					}
				}
				testutil.NativeClick(t, pid, "Exit app (proxy keeps running)")
			}
			err := app.Wait()
			if (err != nil) != (scenario == "missing CLI") {
				t.Fatal("wrong exit result", err)
			}
			files, _ := filepath.Glob(filepath.Join(dir, "logs", "tray_*.txt"))
			if len(files) != 1 || !strings.Contains(testutil.ReadFile(log), "Tray exited;") {
				t.Fatal("errors split the run log", files)
			}
		})
	}
}

func TestTraySingleInstance(t *testing.T) {
	testutil.NativeDesktop(t)
	dir := testutil.IsolatedBuild(t)
	testutil.LocalSSH(t, dir)
	configPath := filepath.Join(dir, "config.toml")
	first := testutil.StartApp(t, dir, configPath, testutil.CLIPath(dir))
	testutil.WaitFor(t, func() bool {
		return strings.Contains(testutil.ReadFile(testutil.TrayLog(dir)), "Proxy status: stopped")
	})
	second := testutil.StartApp(t, dir, configPath, testutil.CLIPath(dir))
	testutil.ExpectDialog(t, second.Process.Pid, "another instance")
	testutil.NativeDismiss(t, second.Process.Pid)
	if err := second.Wait(); err == nil {
		t.Fatal("second instance succeeded")
	}
	if !platform.ProcessAlive(first.Process.Pid) {
		t.Fatal("duplicate instance stopped the existing app")
	}
	testutil.NativeClick(t, first.Process.Pid, "Exit app (proxy keeps running)")
	if err := first.Wait(); err != nil {
		t.Fatal(err)
	}
}

func TestTrayFirstHostKeySetup(t *testing.T) {
	testutil.NativeDesktop(t)
	dir := testutil.IsolatedBuild(t)
	f := testutil.LocalSSH(t, dir)
	text := testutil.ReadFile(filepath.Join(dir, "ssh_config"))
	testutil.WriteFile(t, filepath.Join(dir, "ssh_config"), text[strings.Index(text, "Host primary\n"):])
	testutil.WriteFile(t, f.Trust, "") // Only the ephemeral test server is initially untrusted.
	t.Cleanup(func() { testutil.CallCLI(dir, "stop") })
	before := testutil.NativeTerminals(t)
	app := testutil.StartApp(t, dir, filepath.Join(dir, "config.toml"), testutil.CLIPath(dir))
	testutil.WaitFor(t, func() bool {
		return strings.Contains(testutil.ReadFile(testutil.TrayLog(dir)), "Proxy status: stopped")
	})
	testutil.NativeClick(t, app.Process.Pid, "Start proxy")
	testutil.NativeAcceptHost(t, app.Process.Pid, before, f.Fingerprint)
	testutil.WaitFor(t, func() bool { return testutil.StatusCLI(t, dir).Phase == "running" })
	testutil.NativeTerminalsClosed(t, before)
	if testutil.ReadFile(f.Trust) == "" {
		t.Fatal("confirmation did not persist host trust")
	}
	testutil.MustCLI(t, dir, "trust", "primary") // The same host must now succeed without input.
	testutil.MustCLI(t, dir, "test", "--url", f.Origin.URL)
	testutil.NativeClick(t, app.Process.Pid, "Stop proxy")
	testutil.WaitFor(t, func() bool { return testutil.StatusCLI(t, dir).Phase == "stopped" })
	testutil.NativeClick(t, app.Process.Pid, "Exit app (proxy keeps running)")
	if err := app.Wait(); err != nil {
		t.Fatal(err)
	}
}
