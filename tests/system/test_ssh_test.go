//go:build systemtest

package tests

import (
	"bufio"
	"bytes"
	"context"
	"crypto/tls"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/coder/websocket"
	socks "golang.org/x/net/proxy"

	"ssh_proxy/src/proxy"
	"ssh_proxy/tests/helpers"
)

func TestCLIHTTPSStreamingAndWebSocket(t *testing.T) {
	dir := testutil.IsolatedBuild(t)
	f := testutil.LocalSSH(t, dir)
	t.Cleanup(func() { testutil.CallCLI(dir, "stop") })
	testutil.MustCLI(t, dir, "start", "--wait", "15")
	st := testutil.StatusCLI(t, dir)
	secure := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { io.WriteString(w, "TLS via SSH") }))
	defer secure.Close()
	proxyURL, _ := url.Parse("http://" + st.HTTP)
	dial, err := socks.SOCKS5("tcp", st.SOCKS, nil, socks.Direct)
	if err != nil {
		t.Fatal(err)
	}
	for _, transport := range []*http.Transport{
		{Proxy: http.ProxyURL(proxyURL), TLSClientConfig: &tls.Config{InsecureSkipVerify: true}}, // Local httptest certificate only.
		{DialContext: dial.(socks.ContextDialer).DialContext, TLSClientConfig: &tls.Config{InsecureSkipVerify: true}},
	} {
		client := &http.Client{Transport: transport, Timeout: 5 * time.Second}
		response, err := client.Get(secure.URL)
		if err != nil {
			t.Fatal(err)
		}
		body, err := io.ReadAll(response.Body)
		response.Body.Close()
		transport.CloseIdleConnections()
		if err != nil || string(body) != "TLS via SSH" {
			t.Fatal(string(body), err)
		}
	}
	release := make(chan struct{})
	stream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		io.WriteString(w, "data: first\n\n")
		w.(http.Flusher).Flush()
		<-release
	}))
	defer stream.Close()
	defer close(release)
	transport := &http.Transport{Proxy: http.ProxyURL(proxyURL)}
	defer transport.CloseIdleConnections()
	client := &http.Client{Transport: transport, Timeout: 5 * time.Second}
	response, err := client.Get(stream.URL)
	if err != nil {
		t.Fatal(err)
	}
	line, err := bufio.NewReader(response.Body).ReadString('\n')
	response.Body.Close()
	if err != nil || line != "data: first\n" {
		t.Fatal("stream buffered", line, err)
	}
	web := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := websocket.Accept(w, r, nil)
		if err != nil {
			return
		}
		defer conn.CloseNow()
		kind, data, err := conn.Read(r.Context())
		if err == nil {
			conn.Write(r.Context(), kind, data)
		}
	}))
	defer web.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	conn, _, err := websocket.Dial(ctx, "ws"+strings.TrimPrefix(web.URL, "http"), &websocket.DialOptions{HTTPClient: client})
	if err != nil {
		t.Fatal(err)
	}
	defer conn.CloseNow()
	if err := conn.Write(ctx, websocket.MessageText, []byte("websocket via SSH")); err != nil {
		t.Fatal(err)
	}
	_, data, err := conn.Read(ctx)
	if err != nil || string(data) != "websocket via SSH" {
		t.Fatal(string(data), err)
	}
	testutil.MustCLI(t, dir, "test", "--url", f.Origin.URL)
}

func TestCLITunnelLifecycleAndHealth(t *testing.T) {
	dir := testutil.IsolatedBuild(t)
	f := testutil.LocalSSH(t, dir)
	t.Cleanup(func() { testutil.CallCLI(dir, "stop") })
	testutil.MustCLI(t, dir, "check")
	testutil.MustCLI(t, dir, "trust", "primary") // Known host + key succeeds without prompting.
	if out, err := testutil.CallCLI(dir, "trust", "not-an-endpoint"); err == nil {
		t.Fatal("unknown endpoint accepted", string(out))
	}
	testutil.MustCLI(t, dir, "trust") // First endpoint refuses; second succeeds.
	testutil.MustCLI(t, dir, "start", "--wait", "15")
	before := testutil.StatusCLI(t, dir)
	if before.Phase != "running" || before.Endpoint != "primary" {
		t.Fatalf("not ready: %+v", before)
	}
	testutil.MustCLI(t, dir, "test", "--url", f.Origin.URL)
	testutil.MustCLI(t, dir, "start")
	if testutil.StatusCLI(t, dir).PID != before.PID {
		t.Fatal("duplicate supervisor")
	}
	if !strings.Contains(testutil.ReadFile(before.Log), "Endpoint unavailable failed") {
		t.Fatal("missing ordered failover")
	}
	// A live tunnel with failed probes degrades and recovers without replacement.
	f.Blocked.Store(true)
	testutil.WaitFor(t, func() bool { return testutil.StatusCLI(t, dir).Phase == "degraded" })
	check := exec.Command(testutil.CLIPath(dir), "test", "--url", f.Origin.URL)
	proxy.HideChild(check)
	// HTTP can reuse an upstream connection; SOCKS must open a new SSH channel.
	if out, err := check.Output(); err == nil || !strings.Contains(string(out), "FAIL SOCKS5:") {
		t.Fatalf("failed health checks must report text and exit nonzero: %s %v", out, err)
	}
	if testutil.StatusCLI(t, dir).SSHPID != before.SSHPID {
		t.Fatal("probe failure replaced SSH without opt-in")
	}
	f.Blocked.Store(false)
	testutil.WaitFor(t, func() bool { return testutil.StatusCLI(t, dir).Phase == "running" })
	// Dropping the SSH server must switch endpoints and preserve the supervisor.
	f.Primary.Close()
	testutil.WaitFor(t, func() bool { st := testutil.StatusCLI(t, dir); return st.Phase == "running" && st.Endpoint == "backup" })
	if testutil.StatusCLI(t, dir).PID != before.PID {
		t.Fatal("failover replaced supervisor")
	}
	testutil.MustCLI(t, dir, "test", "--url", f.Origin.URL)
	testutil.MustCLI(t, dir, "restart", "--wait", "15")
	after := testutil.StatusCLI(t, dir)
	if after.PID == before.PID || after.Log == before.Log {
		t.Fatal("restart did not create a new run")
	}
	testutil.MustCLI(t, dir, "test", "--url", f.Origin.URL)
	testutil.MustCLI(t, dir, "stop")
	if testutil.StatusCLI(t, dir).Phase != "stopped" {
		t.Fatal("not stopped")
	}
	testutil.WaitFor(t, func() bool { return !proxy.ProcessAlive(after.SSHPID) })
	if out := string(testutil.MustCLI(t, dir, "logs", "--lines", "100")); !strings.Contains(out, "Stopped; SSH child") {
		t.Fatal(out)
	}
}

func TestCLIProbeFailoverForegroundAndCrashCleanup(t *testing.T) {
	dir := testutil.IsolatedBuild(t)
	f := testutil.LocalSSH(t, dir)
	testutil.WriteFile(t, filepath.Join(dir, "config.toml"), strings.Replace(f.Config, "failover_on_probe_failure = false", "failover_on_probe_failure = true", 1))
	t.Cleanup(func() { testutil.CallCLI(dir, "stop") })
	foreground := exec.Command(testutil.CLIPath(dir), "start", "--foreground")
	proxy.HideChild(foreground)
	out, err := os.Create(filepath.Join(t.TempDir(), "foreground.txt"))
	if err != nil {
		t.Fatal(err)
	}
	defer out.Close()
	var report bytes.Buffer
	foreground.Stdout, foreground.Stderr = &report, out
	if err := foreground.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { foreground.Process.Kill() })
	testutil.WaitFor(t, func() bool { return testutil.StatusCLI(t, dir).Phase == "running" })
	initial := testutil.StatusCLI(t, dir)
	f.Blocked.Store(true)
	testutil.WaitFor(t, func() bool { st := testutil.StatusCLI(t, dir); return st.Phase == "running" && st.Endpoint == "backup" })
	testutil.MustCLI(t, dir, "stop")
	if err := foreground.Wait(); err != nil {
		t.Fatal("foreground shutdown", err)
	}
	if !strings.Contains(report.String(), "Proxy: stopped") || !strings.Contains(report.String(), initial.Log) {
		t.Fatalf("foreground must report stopped state and its run log as text: %s", report.String())
	}
	f.Blocked.Store(false)
	testutil.MustCLI(t, dir, "start", "--wait", "15")
	st := testutil.StatusCLI(t, dir)
	process, err := os.FindProcess(st.PID)
	if err != nil {
		t.Fatal(err)
	}
	if err := process.Kill(); err != nil {
		t.Fatal(err)
	}
	testutil.WaitFor(t, func() bool { return !proxy.ProcessAlive(st.SSHPID) })
	testutil.WaitFor(t, func() bool {
		ln, err := net.Listen("tcp", st.SOCKS)
		if err != nil {
			return false
		}
		ln.Close()
		return true
	})
	testutil.MustCLI(t, dir, "start", "--wait", "15") // A stale state file cannot prevent recovery.
	testutil.MustCLI(t, dir, "test", "--url", f.Origin.URL)
	// Follow stays attached and receives shutdown output from this exact run.
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	follow := exec.CommandContext(ctx, testutil.CLIPath(dir), "logs", "--follow", "--lines", "1")
	proxy.HideChild(follow)
	stream, err := follow.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	if err := follow.Start(); err != nil {
		t.Fatal(err)
	}
	time.Sleep(300 * time.Millisecond)
	testutil.MustCLI(t, dir, "stop")
	done := make(chan bool, 1)
	go func() {
		var b strings.Builder
		buf := make([]byte, 4096)
		for {
			n, err := stream.Read(buf)
			b.Write(buf[:n])
			if strings.Contains(b.String(), "Stopped; SSH child") {
				done <- true
				return
			}
			if err != nil {
				done <- false
				return
			}
		}
	}()
	select {
	case ok := <-done:
		if !ok {
			t.Fatal("follow missed shutdown")
		}
	case <-ctx.Done():
		t.Fatal("follow timed out")
	}
	cancel()
	follow.Wait()
}
