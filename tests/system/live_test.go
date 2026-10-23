//go:build systemtest

package tests

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/BurntSushi/toml"

	"ssh-proxy/src/config"
	"ssh-proxy/src/control"
	"ssh-proxy/src/platform"
	"ssh-proxy/tests/helpers"
)

// Opt-in: real SSH credentials and reachable corporate endpoints are required.
// Each alias must authenticate and forward; the local SSH suite covers the
// lifecycle. Test subprocesses and listener ports are private to this test,
// except an alias's own DynamicForward port. No service or login startup
// entries are installed, and no existing proxy is stopped.
func TestLiveEndpoints(t *testing.T) {
	configPath := os.Getenv("SSH_PROXY_LIVE_CONFIG")
	if configPath == "" {
		t.Skip("optional external endpoint check; pass --live-config")
	}
	base, err := config.LoadConfig(configPath)
	if err != nil {
		t.Fatal(err)
	}
	for _, alias := range base.Endpoints {
		t.Run(alias, func(t *testing.T) {
			dir := testutil.IsolatedBuild(t)
			cfg := *base
			// Only this alias is declared here. A top-level Include supplies every
			// host, ProxyJump hosts included; living beside the original keeps its
			// relative paths. Host trust uses OpenSSH's normal user files.
			sshFile, err := os.CreateTemp(filepath.Dir(base.SSH.ConfigFile), ".ssh-proxy-live-*")
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { os.Remove(sshFile.Name()) })
			_, err = sshFile.WriteString("Include \"" + filepath.ToSlash(base.SSH.ConfigFile) + "\"\nHost " + alias + "\n")
			if err = errors.Join(err, sshFile.Close()); err != nil {
				t.Fatal(err)
			}
			cfg.SSH.ConfigFile = sshFile.Name()
			// An alias's own DynamicForward must keep its port.
			if slices.Contains(config.SSHCommand(base, alias, config.SSHTunnel).Args, "-D") {
				cfg.Proxy.SocksPort = testutil.FreePort(t)
			}
			cfg.Proxy.HTTPPort = testutil.FreePort(t)
			for cfg.Proxy.SocksPort == cfg.Proxy.HTTPPort {
				cfg.Proxy.HTTPPort = testutil.FreePort(t)
			}
			var text strings.Builder
			if err = toml.NewEncoder(&text).Encode(cfg); err != nil {
				t.Fatal(err)
			}
			testutil.WriteFile(t, filepath.Join(dir, "config.toml"), text.String())
			call := func(args ...string) ([]byte, error) {
				ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
				defer cancel()
				cmd := exec.CommandContext(ctx, testutil.CLIPath(dir), args...)
				platform.HideChild(cmd)
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
			status := func() control.Status {
				var st control.Status
				if err := json.Unmarshal(must("status"), &st); err != nil {
					t.Fatal(err)
				}
				return st
			}
			t.Cleanup(func() {
				if out, err := call("stop"); err != nil {
					t.Errorf("cleanup: %v: %s", err, out)
				}
			})
			must("check")
			must("start", "--wait", "55")
			if st := status(); st.Phase != "running" || st.Endpoint != alias {
				t.Fatalf("not ready: %+v", st)
			}
			must("test")
			must("stop")
			if st := status(); st.Phase != "stopped" {
				t.Fatalf("proxy did not stop: %+v", st)
			}
		})
	}
}
