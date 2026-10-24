//go:build systemtest && (windows || darwin)

package tests

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"ssh-proxy/tests/helpers"
)

// Exercise URL discovery and default fallback with a harmless desktop stub.
func TestTrayDesktopLaunchers(t *testing.T) {
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv("SSH_PROXY_TEST_PROCESS", "desktop")
	t.Setenv("SSH_PROXY_URL", "http://127.0.0.1:1") // The script's default takes precedence over inherited variables.
	for _, scenario := range []string{"running", "stopped", "missing-cli"} {
		t.Run(scenario, func(t *testing.T) {
			dir := testutil.IsolatedBuild(t)
			want, pid := "http://127.0.0.1:8181", 0
			switch scenario {
			case "running":
				testutil.LocalSSH(t, dir)
				t.Cleanup(func() { testutil.CallCLI(dir, "stop") })
				testutil.MustCLI(t, dir, "start", "--wait", "15")
				st := testutil.StatusCLI(t, dir)
				want, pid = "http://"+st.HTTP, st.PID
			case "missing-cli":
				if err := os.Remove(testutil.CLIPath(dir)); err != nil {
					t.Fatal(err)
				}
			}
			// Both launchers call one shared helper: each checks its call while
			// running; the helper's fallbacks need only one.
			names := []string{"Codex", "Claude"}
			if scenario != "running" {
				names = names[:1]
			}
			for _, name := range names {
				t.Run(name, func(t *testing.T) {
					record := filepath.Join(dir, name+"-launch.json")
					t.Setenv("SSH_PROXY_TEST_RECORD", record)
					ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
					defer cancel()
					script := filepath.Join(dir, "scripts", name+".sh")
					app := "candidates=('" + strings.ReplaceAll(executable, "'", "'\"'\"'") + "')\n"
					if runtime.GOOS == "windows" {
						script = filepath.Join(dir, "scripts", name+".ps1")
						app = "$candidates = @('" + strings.ReplaceAll(executable, "'", "''") + "')\n"
					}
					// Stub discovery in the isolated copy so this forwarding test never opens a real desktop app.
					contents := testutil.ReadFile(script)
					start, end := strings.Index(contents, "# Locate the"), strings.Index(contents, "# Launch through the shared helper")
					if start < 0 || end <= start {
						t.Fatal("launcher discovery block not found")
					}
					testutil.WriteFile(t, script, contents[:start]+app+contents[end:])
					cmd := exec.CommandContext(ctx, "/bin/bash", script)
					if runtime.GOOS == "windows" {
						cmd = exec.CommandContext(ctx, "powershell.exe", "-NoProfile", "-NonInteractive", "-ExecutionPolicy", "Bypass", "-File", script)
					}
					if out, err := cmd.CombinedOutput(); err != nil {
						t.Fatalf("launcher: %v %s", err, out)
					}
					var launch struct {
						Args  []string
						Proxy string
					}
					testutil.WaitFor(t, func() bool { return json.Unmarshal([]byte(testutil.ReadFile(record)), &launch) == nil })
					if launch.Proxy != want || !strings.Contains(strings.Join(launch.Args, " "), "--proxy-server="+want) {
						t.Fatalf("wrong proxy: launch=%+v; want %s", launch, want)
					}
					if scenario != "missing-cli" && testutil.StatusCLI(t, dir).PID != pid {
						t.Fatal("launcher changed the proxy lifecycle")
					}
				})
			}
		})
	}
}
