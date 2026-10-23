package tests

import (
	"encoding/json"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"ssh-proxy/src/bridge"
	"ssh-proxy/src/cli"
	"ssh-proxy/src/config"
	"ssh-proxy/src/control"
	"ssh-proxy/tests/helpers"
)

func TestCLITestResponseBody(t *testing.T) {
	for _, truncated := range []bool{false, true} {
		name, length, label, wantCode := "complete", "5", "PASS", 0
		if truncated {
			name, length, label, wantCode = "truncated", "100", "FAIL", 1
		}
		t.Run(name, func(t *testing.T) {
			origin := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Length", length)
				io.WriteString(w, "short")
				// Returning closes an incomplete response without completing its body.
			}))
			defer origin.Close()
			socks, _ := testutil.FakeSOCKS(t, strings.TrimPrefix(origin.URL, "http://"))
			b, err := bridge.NewBridge(socks, time.Second)
			if err != nil {
				t.Fatal(err)
			}
			b.SetReady(true)
			proxy := httptest.NewServer(b)
			defer proxy.Close()
			defer b.SetReady(false)
			_, socksPort, _ := net.SplitHostPort(socks)
			_, httpPort, _ := net.SplitHostPort(proxy.Listener.Addr().String())
			text := strings.NewReplacer("socks_port = 1081", "socks_port = "+socksPort,
				"http_port = 8181", "http_port = "+httpPort).Replace(string(testutil.ConfigTemplate(t)))
			p, err := config.PathsFor()
			if err != nil {
				t.Fatal(err)
			}
			// The CLI reads beside Go's temporary test executable. Never overwrite
			// an existing config if this suite is run from a manually built binary.
			install := filepath.Dir(p.Config)
			if _, err := os.Stat(filepath.Join(install, "support")); os.IsNotExist(err) {
				testutil.Support(t, install)
				t.Cleanup(func() { os.RemoveAll(filepath.Join(install, "support")) })
			}
			for name, data := range map[string]string{"config.toml": text,
				"ssh_config": "Host test\n HostName test.invalid\n User tester\n"} {
				path := filepath.Join(filepath.Dir(p.Config), name)
				f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
				if err != nil {
					t.Fatal(err)
				}
				_, err = io.WriteString(f, data)
				f.Close()
				t.Cleanup(func() { os.Remove(path) })
				if err != nil {
					t.Fatal(err)
				}
			}
			out, stderr, code := runCLI(t, []string{"test", "--url", "http://body.invalid/"})
			if code != wantCode || (truncated && !strings.Contains(stderr, "proxy checks failed")) || (!truncated && stderr != "") {
				t.Fatalf("exit=%d stdout=%q stderr=%q", code, out, stderr)
			}
			for _, protocol := range []string{"HTTP proxy", "SOCKS5"} {
				if !strings.Contains(out, label+" "+protocol+": ") {
					t.Fatalf("expected %s for %s: %s", label, protocol, out)
				}
			}
		})
	}
}

func TestStatusProxyURLs(t *testing.T) {
	t.Setenv("SSH_PROXY_RUNTIME", t.TempDir())
	p, err := config.PathsFor()
	if err != nil {
		t.Fatal(err)
	}
	for _, flag := range []string{"--proxy_http", "--proxy_socks"} {
		if out, stderr, code := runCLI(t, []string{"status", flag}); code != 0 || out != "" || stderr != "" {
			t.Fatalf("missing instance: exit=%d stdout=%q stderr=%q", code, out, stderr)
		}
	}
	for _, phase := range []control.Phase{control.PhaseRunning, control.PhaseStarting, control.PhaseConnecting,
		control.PhaseDegraded, control.PhaseReconnecting, control.PhaseStopping, control.PhaseStopped, control.PhaseUnresponsive} {
		t.Run(string(phase), func(t *testing.T) {
			st := control.Status{Phase: phase, PID: os.Getpid(), Config: p.Config, HTTP: "127.0.0.1:18181", SOCKS: "[::1]:11081"}
			state, err := control.NewState(p, "")
			if err != nil {
				t.Fatal(err)
			}
			server := httptest.NewServer(control.ControlHandler(state.Token, func() control.Status { return st }, func() {}))
			defer server.Close()
			state.Address = strings.TrimPrefix(server.URL, "http://")
			if err := control.WriteState(p, state); err != nil {
				t.Fatal(err)
			}
			for _, tc := range []struct{ flag, url string }{
				{"--proxy_http", "http://127.0.0.1:18181\n"},
				{"--proxy_socks", "socks5h://[::1]:11081\n"},
			} {
				want := ""
				if phase == control.PhaseRunning {
					want = tc.url
				}
				out, stderr, code := runCLI(t, []string{"status", tc.flag})
				if code != 0 || out != want || stderr != "" {
					t.Fatalf("%s: exit=%d stdout=%q stderr=%q; want %q", tc.flag, code, out, stderr, want)
				}
			}
			out, stderr, code := runCLI(t, []string{"status"})
			var got control.Status
			if code != 0 || stderr != "" || json.Unmarshal([]byte(out), &got) != nil || got.Phase != phase || got.HTTP != st.HTTP {
				t.Fatalf("plain status changed: exit=%d stdout=%q stderr=%q", code, out, stderr)
			}
		})
	}
}

// Removed or duplicate commands stay rejected before any configuration,
// OS registration or lifecycle side effect. App management belongs to the tray;
// proxy autostart is independent of app startup.
func TestForbiddenCommandsRejectedBeforeOSAccess(t *testing.T) {
	runtime := t.TempDir()
	t.Setenv("SSH_PROXY_RUNTIME", runtime)
	for _, tc := range []struct {
		args []string
		want string
	}{
		{[]string{"tray"}, `unknown command "tray"`},
		{[]string{"tray", "--hidden"}, `unknown command "tray"`},
		{[]string{"trust"}, `unknown command "trust"`},
		{[]string{"startup"}, `unknown command "startup"`},
		{[]string{"startup", "enable"}, `unknown command "startup"`},
		{[]string{"startup", "disable"}, `unknown command "startup"`},
		{[]string{"startup", "status"}, `unknown command "startup"`},
		{[]string{"help"}, `unknown command "help"`},
		{[]string{"version"}, `unknown command "version"`},
		{[]string{"completion"}, `unknown command "completion"`},
		{[]string{"service", "install"}, `unknown command "service"`},
		{[]string{"service", "status"}, `unknown command "service"`},
		{[]string{"_service"}, `unknown command "_service"`},
		{[]string{"service", "start"}, `unknown command "service"`},
		{[]string{"service", "stop"}, `unknown command "service"`},
		{[]string{"service", "restart"}, `unknown command "service"`},
		// status and check always print JSON; the other commands print text.
		{[]string{"status", "--json"}, "unknown flag: --json"},
		{[]string{"check", "--json"}, "unknown flag: --json"},
		{[]string{"start", "--json"}, "unknown flag: --json"},
		{[]string{"stop", "--json"}, "unknown flag: --json"},
		{[]string{"logs", "--json"}, "unknown flag: --json"},
		{[]string{"login", "--json"}, "unknown flag: --json"},
		{[]string{"autostart", "status", "--json"}, "unknown flag: --json"},
	} {
		t.Run(strings.Join(tc.args, "-"), func(t *testing.T) {
			err := cli.Run(tc.args)
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("want %q, got %v", tc.want, err)
			}
		})
	}
	if entries, err := os.ReadDir(runtime); err != nil || len(entries) != 0 {
		t.Fatalf("rejected commands wrote runtime state: %v %v", entries, err)
	}
}
