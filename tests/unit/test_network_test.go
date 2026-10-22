package tests

import (
	"bufio"
	"context"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/coder/websocket"

	"ssh_proxy/src/proxy"
	"ssh_proxy/tests/helpers"
)

func TestChunkedStreamAndTrailers(t *testing.T) {
	release := make(chan struct{})
	origin := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/x-ndjson")
		w.Header().Set("Trailer", "X-Checksum")
		io.WriteString(w, "first\n")
		w.(http.Flusher).Flush()
		<-release
		io.WriteString(w, "last\n")
		w.Header().Set("X-Checksum", "verified")
	}))
	defer origin.Close()
	_, server, _ := testutil.BridgeFixture(t, strings.TrimPrefix(origin.URL, "http://"))
	pu, _ := url.Parse(server.URL)
	client := &http.Client{Transport: &http.Transport{Proxy: http.ProxyURL(pu)}, Timeout: 3 * time.Second}
	defer client.CloseIdleConnections()
	resp, err := client.Get("http://stream.invalid/data")
	if err != nil {
		close(release)
		t.Fatal(err)
	}
	defer resp.Body.Close()
	reader := bufio.NewReader(resp.Body)
	line, err := reader.ReadString('\n')
	close(release)
	if err != nil || line != "first\n" {
		t.Fatalf("first chunk was buffered: %q %v", line, err)
	}
	rest, err := io.ReadAll(reader)
	if err != nil || string(rest) != "last\n" || resp.Trailer.Get("X-Checksum") != "verified" {
		t.Fatalf("lost body/trailer: %q %v %v", rest, resp.Trailer, err)
	}
}

func TestWebSocketOverHTTPProxy(t *testing.T) {
	origin := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		c, err := websocket.Accept(w, r, nil)
		if err != nil {
			return
		}
		defer c.CloseNow()
		c.SetReadLimit(1024 * 1024)
		kind, data, err := c.Read(r.Context())
		if err == nil {
			c.Write(r.Context(), kind, data)
		}
	}))
	defer origin.Close()
	_, server, _ := testutil.BridgeFixture(t, strings.TrimPrefix(origin.URL, "http://"))
	pu, _ := url.Parse(server.URL)
	client := &http.Client{Transport: &http.Transport{Proxy: http.ProxyURL(pu)}}
	defer client.CloseIdleConnections()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	c, _, err := websocket.Dial(ctx, "ws://socket.invalid/echo", &websocket.DialOptions{HTTPClient: client})
	if err != nil {
		t.Fatal(err)
	}
	defer c.CloseNow()
	payload := strings.Repeat("websocket-data", 2000)
	if err = c.Write(ctx, websocket.MessageText, []byte(payload)); err != nil {
		t.Fatal(err)
	}
	c.SetReadLimit(1024 * 1024)
	_, got, err := c.Read(ctx)
	if err != nil || string(got) != payload {
		t.Fatalf("WebSocket payload changed: %d %v", len(got), err)
	}
}

func TestConcurrentRequestsAndEnvironmentCannotBypassSOCKS(t *testing.T) {
	t.Setenv("HTTP_PROXY", "http://127.0.0.1:1")
	t.Setenv("HTTPS_PROXY", "http://127.0.0.1:1")
	t.Setenv("NO_PROXY", "*")
	body := strings.Repeat("concurrent-response", 4096)
	origin := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { io.WriteString(w, body) }))
	defer origin.Close()
	_, server, targets := testutil.BridgeFixture(t, strings.TrimPrefix(origin.URL, "http://"))
	// Drain fixture observations so they don't become a concurrency bottleneck.
	done := make(chan struct{})
	defer close(done)
	go func() {
		for {
			select {
			case <-targets:
			case <-done:
				return
			}
		}
	}()
	pu, _ := url.Parse(server.URL)
	client := &http.Client{Transport: &http.Transport{Proxy: http.ProxyURL(pu)}, Timeout: 10 * time.Second}
	defer client.CloseIdleConnections()
	var wg sync.WaitGroup
	for range 32 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			resp, err := client.Get("http://concurrent.invalid/")
			if err != nil {
				t.Error(err)
				return
			}
			defer resp.Body.Close()
			got, err := io.ReadAll(resp.Body)
			if err != nil || string(got) != body {
				t.Errorf("response corrupted: %d %v", len(got), err)
			}
		}()
	}
	wg.Wait()
}

func TestStalledSOCKSHandshakeTimesOut(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	done := make(chan struct{})
	defer close(done)
	go func() {
		c, err := ln.Accept()
		if err == nil {
			defer c.Close()
			<-done
		}
	}()
	bridge, err := proxy.NewBridge(ln.Addr().String(), 100*time.Millisecond)
	if err != nil {
		t.Fatal(err)
	}
	start := time.Now()
	if err = bridge.Probe(context.Background(), "unresolved.invalid:443", time.Second); err == nil {
		t.Fatal("stalled SOCKS accepted")
	}
	if time.Since(start) > 2*time.Second {
		t.Fatal("SOCKS handshake ignored dial timeout")
	}
}

func TestProbeAllowsOneUnavailableTarget(t *testing.T) {
	origin := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	defer origin.Close()
	address, _ := testutil.FakeSOCKS(t, strings.TrimPrefix(origin.URL, "http://"))
	bridge, err := proxy.NewBridge(address, time.Second)
	if err != nil {
		t.Fatal(err)
	}
	if err = proxy.ProbeTargets(context.Background(), bridge, []string{"invalid-address", "working.invalid:443"}, time.Second); err != nil {
		t.Fatal(err)
	}
}
