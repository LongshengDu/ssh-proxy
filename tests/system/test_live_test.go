//go:build systemtest

package tests

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"testing"
	"time"

	"ssh_proxy/src/proxy"
	"ssh_proxy/tests/helpers"
)

// Opt-in: real SSH credentials and reachable corporate endpoints are required.
// Test subprocesses and listener ports are private to this test. No service or
// login startup entries are installed, and no existing proxy is stopped.
func TestLiveEndpoints(t *testing.T) {
	configPath := os.Getenv("SSH_PROXY_LIVE_CONFIG")
	if configPath == "" {
		t.Skip("optional external endpoint check; pass --live-config")
	}
	output := testutil.SystemBuild(t)
	exe := testutil.CLIPath(output)
	template, err := os.ReadFile(configPath)
	if err != nil {
		t.Fatal(err)
	}
	base, err := proxy.LoadConfig(configPath)
	if err != nil {
		t.Fatal(err)
	}
	for index, alias := range base.Endpoints {
		t.Run(alias, func(t *testing.T) {
			dir := t.TempDir()
			config := filepath.Join(dir, "config.toml")
			text := string(template)
			socks, httpPort := strconv.Itoa(testutil.FreePort(t)), strconv.Itoa(testutil.FreePort(t))
			for socks == httpPort {
				httpPort = strconv.Itoa(testutil.FreePort(t))
			}
			text = strings.Replace(text, "socks_port = "+strconv.Itoa(base.Proxy.SocksPort), "socks_port = "+socks, 1)
			text = strings.Replace(text, "http_port = "+strconv.Itoa(base.Proxy.HTTPPort), "http_port = "+httpPort, 1)
			if err = os.WriteFile(config, []byte(text), 0600); err != nil {
				t.Fatal(err)
			}
			// Candidates are only declared here. Native Include resolves the actual
			// private connection data; host trust uses OpenSSH's normal user files.
			sshText := "Host unavailable-test\n HostName 127.0.0.1\n Port 9\n User test\nHost " + alias + "\n Include \"" + filepath.ToSlash(base.SSH.ConfigFile) + "\"\nHost *\n DynamicForward 127.0.0.1:" + socks + "\n"
			if err = os.WriteFile(filepath.Join(dir, "ssh_config"), []byte(sshText), 0600); err != nil {
				t.Fatal(err)
			}
			call := func(args ...string) ([]byte, error) {
				ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
				defer cancel()
				cmd := exec.CommandContext(ctx, exe, append(args, "--config", config)...)
				proxy.HideChild(cmd)
				return cmd.CombinedOutput()
			}
			must := func(args ...string) []byte {
				out, err := call(args...)
				if err != nil {
					t.Fatalf("%v: %v\n%s", args, err, out)
				}
				t.Logf("%v: %s", args, strings.TrimSpace(string(out)))
				return out
			}
			t.Cleanup(func() {
				if out, err := call("stop"); err != nil {
					t.Errorf("cleanup: %v: %s", err, out)
				}
			})
			must("check")
			must("start", "--wait", "55")
			status := func() proxy.Status {
				var st proxy.Status
				out, err := call("status")
				if err != nil {
					t.Fatal(err)
				}
				if err = json.Unmarshal(out, &st); err != nil {
					t.Fatal(err)
				}
				return st
			}
			before := status()
			if before.Phase != "running" || before.Endpoint != alias {
				t.Fatalf("not ready: %+v", before)
			}
			must("test")
			must("test", "--url", "http://example.com/")
			must("start")
			if status().PID != before.PID {
				t.Fatal("duplicate supervisor")
			}
			log, err := os.ReadFile(before.Log)
			if err != nil || !strings.Contains(string(log), "Endpoint unavailable-test failed") {
				t.Fatalf("ordered failover missing: %v", err)
			}
			tray := filepath.Join(output, "ssh_proxy_app.exe")
			if runtime.GOOS == "darwin" {
				tray = filepath.Join(output, "ssh_proxy_app.app", "Contents", "MacOS", "ssh_proxy_app")
			}
			if _, err = os.Stat(tray); runtime.GOOS != "linux" && err == nil {
				ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
				defer cancel()
				cmd := exec.CommandContext(ctx, tray, "--config", config, "--backend", exe, "--smoke-test")
				proxy.HideChild(cmd)
				if out, err := cmd.CombinedOutput(); err != nil {
					t.Fatalf("tray: %v: %s", err, out)
				}
				if status().PID != before.PID {
					t.Fatal("tray exit interrupted forwarding")
				}
			}
			must("restart", "--wait", "55")
			must("test")
			must("stop")
			if status().Phase != "stopped" {
				t.Fatal("proxy did not stop")
			}
			if index == 0 {
				ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
				defer cancel()
				foreground := exec.CommandContext(ctx, exe, "start", "--foreground", "--config", config)
				proxy.HideChild(foreground)
				logfile, err := os.Create(filepath.Join(output, "logs", "live-foreground.log"))
				if err != nil {
					t.Fatal(err)
				}
				defer logfile.Close()
				foreground.Stdout, foreground.Stderr = logfile, logfile
				if err = foreground.Start(); err != nil {
					t.Fatal(err)
				}
				deadline := time.Now().Add(60 * time.Second)
				for status().Phase != "running" && time.Now().Before(deadline) {
					time.Sleep(300 * time.Millisecond)
				}
				if status().Phase != "running" {
					t.Fatal("foreground did not become ready")
				}
				must("test")
				must("stop")
				if err = foreground.Wait(); err != nil {
					t.Fatalf("foreground shutdown: %v", err)
				}
			}
			missing := exec.Command(exe, "--config", filepath.Join(dir, "absent.toml"), "start")
			proxy.HideChild(missing)
			if err = missing.Run(); err == nil {
				t.Fatal("missing configuration accepted")
			}
		})
	}
}
