package tests

import (
	"encoding/json"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"ssh_proxy/src/cli"
	"ssh_proxy/src/config"
	"ssh_proxy/src/control"
)

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
// service lifecycle uses the ordinary start, stop and restart commands.
func TestForbiddenCommandsRejectedBeforeOSAccess(t *testing.T) {
	runtime := t.TempDir()
	t.Setenv("SSH_PROXY_RUNTIME", runtime)
	for _, tc := range []struct {
		args []string
		want string
	}{
		{[]string{"tray"}, `unknown command "tray"`},
		{[]string{"tray", "--hidden"}, `unknown command "tray"`},
		{[]string{"login"}, `unknown command "login"`},
		{[]string{"startup"}, `unknown command "startup"`},
		{[]string{"startup", "enable"}, `unknown command "startup"`},
		{[]string{"startup", "disable"}, `unknown command "startup"`},
		{[]string{"startup", "status"}, `unknown command "startup"`},
		{[]string{"help"}, `unknown command "help"`},
		{[]string{"version"}, `unknown command "version"`},
		{[]string{"completion"}, `unknown command "completion"`},
		{[]string{"service", "start"}, `unknown command "start" for "ssh_proxy_cli service"`},
		{[]string{"service", "stop"}, `unknown command "stop" for "ssh_proxy_cli service"`},
		{[]string{"service", "restart"}, `unknown command "restart" for "ssh_proxy_cli service"`},
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
