package tests

import (
	"bufio"
	"bytes"
	"context"
	"crypto/tls"
	"crypto/x509"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"ssh_proxy/src/bridge"
	"ssh_proxy/src/config"
	"ssh_proxy/src/control"
	"ssh_proxy/src/logs"
	"ssh_proxy/src/platform"
	"ssh_proxy/src/supervisor"
	"ssh_proxy/tests/helpers"
)

// A state file left by a hard stop may name a live, unrelated PID. Nothing holds
// the lock, so stop has nothing to stop and never kills by PID. Windows reuses
// PIDs quickly, so its status trusts the lock; Unix status keeps the PID check.
func TestStaleStateDoesNotAuthorizeKillingLiveProcess(t *testing.T) {
	dir := t.TempDir()
	p := config.Paths{Config: filepath.Join(dir, "config.toml"), State: filepath.Join(dir, "state.json"), Logs: dir, ID: fmt.Sprintf("stale%011x", os.Getpid())}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { http.Error(w, "not a proxy", http.StatusUnauthorized) }))
	defer server.Close()
	state, err := control.NewState(p, strings.TrimPrefix(server.URL, "http://"))
	if err != nil {
		t.Fatal(err)
	}
	if err := control.WriteState(p, state); err != nil {
		t.Fatal(err)
	}
	st := control.QueryStatus(p)
	if runtime.GOOS == "windows" {
		if st.Phase != control.PhaseStopped || !st.UncleanExit || st.PID != 0 {
			t.Fatalf("reused PID must not look alive: %+v", st)
		}
	} else if st.Phase != control.PhaseUnresponsive || st.PID != os.Getpid() {
		t.Fatalf("wrong stale status: %+v", st)
	}
	if err := supervisor.StopBackground(p); err != nil {
		t.Fatal("stop without a supervisor must succeed:", err)
	}
	if _, err := os.Stat(p.State); err != nil {
		t.Fatal("only the lock holder may remove state", err)
	}
}

// A supervisor that holds the lock but has not published its control API yet
// is still starting: stop waits for the API and stops it, never reporting a
// stopped proxy that then starts.
func TestStopWaitsForStartingSupervisor(t *testing.T) {
	dir := t.TempDir()
	p := config.Paths{Config: filepath.Join(dir, "config.toml"), State: filepath.Join(dir, "state.json"), Logs: dir, ID: fmt.Sprintf("start%011x", os.Getpid())}
	unlock, err := platform.LockInstance(p.ID)
	if err != nil {
		t.Fatal(err)
	}
	var release sync.Once
	defer release.Do(unlock)
	var handler http.Handler
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { handler.ServeHTTP(w, r) }))
	defer server.Close()
	state, err := control.NewState(p, strings.TrimPrefix(server.URL, "http://"))
	if err != nil {
		t.Fatal(err)
	}
	handler = control.ControlHandler(state.Token, func() control.Status {
		return control.Status{Phase: control.PhaseConnecting, PID: state.PID, Config: p.Config}
	}, func() { release.Do(unlock) })
	done := make(chan error, 1)
	go func() { done <- supervisor.StopBackground(p) }()
	select {
	case err := <-done:
		t.Fatal("stop returned while the supervisor was starting:", err)
	case <-time.After(700 * time.Millisecond):
	}
	if err := control.WriteState(p, state); err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("stop did not finish after the control API appeared")
	}
	if running, err := platform.InstanceRunning(p.ID); err != nil || running {
		t.Fatal("supervisor lock still held", running, err)
	}
}

func TestHTTPForwardingBodyHeadersAndRemoteDNS(t *testing.T) {
	body := strings.Repeat("request-body-", 5000)
	origin := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got, _ := io.ReadAll(r.Body)
		if string(got) != body || r.URL.RequestURI() != "/hello?q=world" || r.Host != "does-not-resolve.invalid" {
			t.Errorf("forwarded request changed: %s %s len=%d", r.Host, r.URL.RequestURI(), len(got))
		}
		if r.Header.Get("Proxy-Authorization") != "" || r.Header.Get("X-Hop") != "" {
			t.Error("hop/proxy credentials leaked to origin")
		}
		w.Header().Set("Connection", "X-Response-Hop")
		w.Header().Set("X-Response-Hop", "remove")
		w.Header().Set("X-End-To-End", "preserve")
		w.Write([]byte("received"))
	}))
	defer origin.Close()
	_, server, targets := testutil.BridgeFixture(t, strings.TrimPrefix(origin.URL, "http://"))
	proxyURL, _ := url.Parse(server.URL)
	client := &http.Client{Transport: &http.Transport{Proxy: http.ProxyURL(proxyURL)}, Timeout: 5 * time.Second}
	defer client.CloseIdleConnections()
	req, _ := http.NewRequest("POST", "http://does-not-resolve.invalid/hello?q=world", strings.NewReader(body))
	req.Header.Set("Proxy-Authorization", "Basic secret")
	req.Header.Set("Connection", "X-Hop")
	req.Header.Set("X-Hop", "remove")
	resp, err := client.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	data, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != 200 || string(data) != "received" || resp.Header.Get("X-Response-Hop") != "" || resp.Header.Get("X-End-To-End") != "preserve" {
		t.Fatalf("bad forwarded response: %s %q %v", resp.Status, data, resp.Header)
	}
	if target := <-targets; target != "does-not-resolve.invalid:80" {
		t.Fatalf("expected hostname to reach SOCKS: %s", target)
	}
}

func TestHTTPSConnectPreservesTLS(t *testing.T) {
	origin := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.Write([]byte("secure response")) }))
	defer origin.Close()
	_, server, _ := testutil.BridgeFixture(t, strings.TrimPrefix(origin.URL, "https://"))
	proxyURL, _ := url.Parse(server.URL)
	roots := x509.NewCertPool()
	roots.AddCert(origin.Certificate())
	client := &http.Client{Transport: &http.Transport{Proxy: http.ProxyURL(proxyURL), TLSClientConfig: &tls.Config{RootCAs: roots}}, Timeout: 5 * time.Second}
	defer client.CloseIdleConnections()
	resp, err := client.Get(origin.URL)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	data, _ := io.ReadAll(resp.Body)
	if string(data) != "secure response" || resp.TLS == nil {
		t.Fatalf("TLS tunnel failed: %q", data)
	}
}

func TestConnectKeepsBufferedBytesAndClosesOnDisconnect(t *testing.T) {
	origin := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.Write([]byte("pipelined")) }))
	defer origin.Close()
	b, server, _ := testutil.BridgeFixture(t, strings.TrimPrefix(origin.URL, "http://"))
	client, err := net.Dial("tcp", strings.TrimPrefix(server.URL, "http://"))
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()
	client.SetDeadline(time.Now().Add(5 * time.Second))
	io.WriteString(client, "CONNECT remote.invalid:80 HTTP/1.1\r\nHost: remote.invalid:80\r\n\r\nGET / HTTP/1.1\r\nHost: remote.invalid\r\n\r\n")
	reader := bufio.NewReader(client)
	resp, err := http.ReadResponse(reader, &http.Request{Method: "CONNECT"})
	if err != nil || resp.StatusCode != 200 {
		t.Fatalf("CONNECT: %v %v", resp, err)
	}
	resp, err = http.ReadResponse(reader, &http.Request{Method: "GET"})
	if err != nil {
		t.Fatal(err)
	}
	data, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if string(data) != "pipelined" {
		t.Fatalf("lost buffered bytes: %q", data)
	}
	b.SetReady(false)
	client.SetReadDeadline(time.Now().Add(time.Second))
	_, err = reader.ReadByte()
	if err == nil {
		t.Fatal("tunnel remained open after disconnect")
	}
	if e, ok := err.(net.Error); ok && e.Timeout() {
		t.Fatal("tunnel was not closed")
	}
}

func TestBridgeFailsClosed(t *testing.T) {
	var calls atomic.Int32
	origin := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { calls.Add(1) }))
	defer origin.Close()
	b, err := bridge.NewBridge("127.0.0.1:1", 100*time.Millisecond)
	if err != nil {
		t.Fatal(err)
	}
	for _, ready := range []bool{false, true} {
		b.SetReady(ready)
		request := httptest.NewRequest("GET", origin.URL, nil)
		response := httptest.NewRecorder()
		b.ServeHTTP(response, request)
		want := 503
		if ready {
			want = 502
		}
		if response.Code != want {
			t.Fatalf("want %d got %d", want, response.Code)
		}
	}
	if calls.Load() != 0 {
		t.Fatal("request bypassed SOCKS and reached origin directly")
	}
}

func TestStreamingResponseIsFlushed(t *testing.T) {
	release := make(chan struct{})
	origin := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		io.WriteString(w, "data: first\n\n")
		w.(http.Flusher).Flush()
		<-release
	}))
	defer func() { close(release); origin.Close() }()
	_, server, _ := testutil.BridgeFixture(t, strings.TrimPrefix(origin.URL, "http://"))
	proxyURL, _ := url.Parse(server.URL)
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	req, _ := http.NewRequestWithContext(ctx, "GET", "http://stream.invalid/", nil)
	client := &http.Client{Transport: &http.Transport{Proxy: http.ProxyURL(proxyURL)}}
	defer client.CloseIdleConnections()
	resp, err := client.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	line, err := bufio.NewReader(resp.Body).ReadString('\n')
	if err != nil || line != "data: first\n" {
		t.Fatalf("stream was buffered: %q %v", line, err)
	}
}

func TestConfigValidationAndRelativePaths(t *testing.T) {
	template := testutil.ConfigTemplate(t)
	dir := t.TempDir()
	sshConfig := []byte("Host proxy-primary proxy-backup\n HostName example.invalid\n User tester\n")
	if err := os.WriteFile(filepath.Join(dir, "ssh_config"), sshConfig, 0600); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "config.toml")
	if err := os.WriteFile(path, template, 0600); err != nil {
		t.Fatal(err)
	}
	cfg, err := config.LoadConfig(path)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.SSH.ConfigFile != filepath.Join(dir, "ssh_config") || cfg.Endpoints[0] != "proxy-primary" || cfg.Endpoints[1] != "proxy-backup" {
		t.Fatal("relative paths or endpoint order changed")
	}
	for _, tc := range []struct{ name, from, to string }{
		{"nonloopback", `bind = "127.0.0.1"`, `bind = "0.0.0.0"`},
		{"sameports", `http_port = 8181`, `http_port = 1081`},
		{"missing-setting", `http_port = 8181`, `http_prt = 8181`},
		{"badtimeout", `timeout_seconds = 8`, `timeout_seconds = 0`},
		{"bad-ssh-timeout", `connect_timeout_seconds = 10`, `connect_timeout_seconds = 0`},
		{"bad-ssh-attempts", `connection_attempts = 1`, `connection_attempts = 0`},
		{"bad-ssh-keepalive", `server_alive_interval_seconds = 15`, `server_alive_interval_seconds = 0`},
		{"bad-ssh-count", `server_alive_count_max = 3`, `server_alive_count_max = 0`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			os.WriteFile(path, []byte(strings.Replace(string(template), tc.from, tc.to, 1)), 0600)
			if _, err := config.LoadConfig(path); err == nil {
				t.Fatal("invalid config accepted")
			}
		})
	}
	// Settings the program no longer uses are ignored.
	for _, tc := range []struct{ name, from, to string }{
		{"obsolete-trust-path", `[ssh]`, "[ssh]\nknown_hosts_file = \"known_hosts\""},
		{"obsolete-alias-list", `[proxy]`, "endpoints = [\"unused\"]\n[proxy]"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			os.WriteFile(path, []byte(strings.Replace(string(template), tc.from, tc.to, 1)), 0600)
			if _, err := config.LoadConfig(path); err != nil {
				t.Fatal("unused setting rejected:", err)
			}
		})
	}
}

func TestControlAuthenticationAndStop(t *testing.T) {
	stopped := make(chan struct{}, 1)
	handler := control.ControlHandler("test-secret", func() control.Status { return control.Status{Phase: "running", PID: 123, Config: "test"} }, func() { stopped <- struct{}{} })
	request := httptest.NewRequest("POST", "http://localhost/stop", nil)
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != 401 {
		t.Fatal("unauthenticated stop accepted")
	}
	request.Header.Set("Authorization", "Bearer test-secret")
	response = httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != 200 {
		t.Fatal(response.Code)
	}
	if !response.Flushed {
		t.Fatal("stop response was not flushed before shutdown")
	}
	select {
	case <-stopped:
	case <-time.After(time.Second):
		t.Fatal("stop was not called")
	}
}

func TestInstanceLockAndContinuousRunLog(t *testing.T) {
	id := "test-" + strconv.Itoa(os.Getpid())
	unlock, err := platform.LockInstance(id)
	if err != nil {
		t.Fatal(err)
	}
	if release, err := platform.LockInstance(id); err == nil {
		release()
		unlock()
		t.Fatal("duplicate instance accepted")
	}
	unlock()
	unlock, err = platform.LockInstance(id)
	if err != nil {
		t.Fatal(err)
	}
	unlock()
	dir := filepath.Join(t.TempDir(), "portable [logs]")
	p, err := config.PathsForExecutable(filepath.Join(dir, "ssh_proxy_cli"))
	if err != nil {
		t.Fatal(err)
	}
	_, writer, err := logs.NewRunLog(p, "logs", false)
	if err != nil {
		t.Fatal(err)
	}
	path := writer.Name()
	first := bytes.Repeat([]byte("a"), 6*1024*1024)
	if _, err := writer.Write(first); err != nil {
		t.Fatal(err)
	}
	if _, err := writer.Write([]byte("first error\n")); err != nil {
		t.Fatal(err)
	}
	writer.Close()
	_, resumed, err := logs.NewLog(path, false)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := resumed.Write([]byte("second error\n")); err != nil {
		t.Fatal(err)
	}
	resumed.Close()
	current, _ := os.ReadFile(path)
	files, _ := os.ReadDir(p.Logs)
	if len(files) != 1 || !bytes.Equal(current, append(first, []byte("first error\nsecond error\n")...)) {
		t.Fatal("one run must preserve all normal output and errors in one file")
	}
	_, next, err := logs.NewRunLog(p, "logs", false)
	if err != nil {
		t.Fatal(err)
	}
	next.Close()
	found, err := config.PathsForExecutable(filepath.Join(dir, "ssh_proxy_cli"))
	if err != nil || found.Log != next.Name() || found.Log == path {
		t.Fatal("new process run needs a new discoverable file", found, err)
	}
}

func TestSSHJobCleanup(t *testing.T) {
	if os.Getenv("SSH_PROXY_JOB_HELPER") == "1" {
		time.Sleep(30 * time.Second)
		return
	}
	cmd := exec.Command(os.Args[0], "-test.run=^TestSSHJobCleanup$")
	cmd.Env = append(os.Environ(), "SSH_PROXY_JOB_HELPER=1")
	platform.HideChild(cmd)
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	defer cmd.Process.Kill()
	job, err := platform.ContainChild(cmd)
	if err != nil {
		t.Fatal(err)
	}
	platform.CloseJob(job)
	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()
	select {
	case <-done:
		// Windows can report exit code zero for kill-on-job-close termination.
		if platform.ProcessAlive(cmd.Process.Pid) {
			t.Fatal("child is still active after job close")
		}
	case <-time.After(3 * time.Second):
		t.Fatal("child survived job close")
	}
}

func TestConfigFlagsRejected(t *testing.T) {
	t.Setenv("SSH_PROXY_RUNTIME", t.TempDir())
	path := filepath.Join(t.TempDir(), "space path", "config.toml")
	for _, args := range [][]string{
		{"check", "-c", path}, {"-c", path, "check"}, {"check", "--config", path}, {"--config=" + path, "check"},
		{"-c=" + path, "check"}, {"check", "-c=" + path},
	} {
		if _, stderr, code := runCLI(t, args); code == 0 || !strings.Contains(stderr, "unknown") {
			t.Fatalf("removed config flag accepted: %v: %s", args, stderr)
		}
	}
}

func TestProbeRequiresWorkingSOCKS(t *testing.T) {
	b, err := bridge.NewBridge("127.0.0.1:1", 100*time.Millisecond)
	if err != nil {
		t.Fatal(err)
	}
	if err := b.Probe(context.Background(), "example.com:443", time.Second); err == nil {
		t.Fatal("unusable tunnel marked ready")
	}
}
