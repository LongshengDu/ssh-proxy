package tests

import (
	"bufio"
	"bytes"
	"compress/gzip"
	"context"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/coder/websocket"

	"ssh_proxy/src/bridge"
	"ssh_proxy/tests/helpers"
)

func TestHTTPPreservesEncodingAndBody(t *testing.T) {
	plain := []byte(strings.Repeat("download content\n", 100))
	var compressed bytes.Buffer
	zip := gzip.NewWriter(&compressed)
	zip.Write(plain)
	zip.Close()
	origin := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		encoding := r.Header.Get("Accept-Encoding")
		w.Header().Set("X-Seen-Encoding", encoding)
		body := plain
		if encoding == "gzip" {
			body = compressed.Bytes()
			w.Header().Set("Content-Encoding", "gzip")
		}
		w.Header().Set("Content-Length", strconv.Itoa(len(body)))
		w.Write(body)
	}))
	defer origin.Close()
	_, server, _ := testutil.BridgeFixture(t, strings.TrimPrefix(origin.URL, "http://"))
	proxyURL, _ := url.Parse(server.URL)
	client := &http.Client{Transport: &http.Transport{Proxy: http.ProxyURL(proxyURL), DisableCompression: true}, Timeout: 5 * time.Second}
	defer client.CloseIdleConnections()
	for _, encoding := range []string{"", "identity", "br", "gzip"} {
		t.Run(encoding, func(t *testing.T) {
			req, _ := http.NewRequest("GET", "http://encoding.invalid/download", nil)
			if encoding != "" {
				req.Header.Set("Accept-Encoding", encoding)
			}
			resp, err := client.Do(req)
			if err != nil {
				t.Fatal(err)
			}
			defer resp.Body.Close()
			body, err := io.ReadAll(resp.Body)
			want, contentEncoding := plain, ""
			if encoding == "gzip" {
				want, contentEncoding = compressed.Bytes(), "gzip"
			}
			if err != nil || !bytes.Equal(body, want) || resp.ContentLength != int64(len(want)) || resp.Header.Get("Content-Encoding") != contentEncoding || resp.Header.Get("X-Seen-Encoding") != encoding {
				t.Fatalf("proxy changed encoding/body: headers=%v bytes=%d error=%v", resp.Header, len(body), err)
			}
		})
	}
}

func TestHTTPReusesConnectionsAcrossConcurrentBursts(t *testing.T) {
	const clients = 12
	arrived, release := make(chan struct{}, clients), make(chan struct{})
	var connections atomic.Int32
	origin := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		arrived <- struct{}{}
		<-release
		io.WriteString(w, "ok")
	}))
	origin.Config.ConnState = func(_ net.Conn, state http.ConnState) {
		if state == http.StateNew {
			connections.Add(1)
		}
	}
	origin.Start()
	defer origin.Close()
	defer close(release)
	_, server, _ := testutil.BridgeFixture(t, strings.TrimPrefix(origin.URL, "http://"))
	proxyURL, _ := url.Parse(server.URL)
	client := &http.Client{Transport: &http.Transport{Proxy: http.ProxyURL(proxyURL), MaxIdleConnsPerHost: clients}, Timeout: 5 * time.Second}
	defer client.CloseIdleConnections()
	for range 2 {
		done := make(chan error, clients)
		for range clients {
			go func() {
				resp, err := client.Get("http://pool.invalid/")
				if err == nil {
					_, err = io.Copy(io.Discard, resp.Body)
					resp.Body.Close()
				}
				done <- err
			}()
		}
		deadline := time.After(5 * time.Second)
		for range clients {
			select {
			case <-arrived:
			case <-deadline:
				t.Fatal("concurrent requests did not reach the origin")
			}
		}
		for range clients {
			release <- struct{}{}
		}
		for range clients {
			if err := <-done; err != nil {
				t.Fatal(err)
			}
		}
	}
	if got := connections.Load(); got != clients {
		t.Fatalf("second burst opened new upstream connections: got %d, want %d", got, clients)
	}
}

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

func TestInterruptedHTTPBodyFails(t *testing.T) {
	for _, framing := range []string{"chunked", "content-length"} {
		t.Run(framing, func(t *testing.T) {
			origin, err := net.Listen("tcp", "127.0.0.1:0")
			if err != nil {
				t.Fatal(err)
			}
			defer origin.Close()
			go func() {
				c, err := origin.Accept()
				if err != nil {
					return
				}
				defer c.Close()
				c.SetDeadline(time.Now().Add(3 * time.Second))
				if _, err = http.ReadRequest(bufio.NewReader(c)); err != nil {
					return
				}
				body := "Content-Length: 20\r\n\r\nhello"
				if framing == "chunked" {
					body = "Transfer-Encoding: chunked\r\n\r\n5\r\nhello\r\n"
				}
				io.WriteString(c, "HTTP/1.1 200 OK\r\n"+body)
			}()
			_, server, _ := testutil.BridgeFixture(t, origin.Addr().String())
			proxyURL, _ := url.Parse(server.URL)
			client := &http.Client{Transport: &http.Transport{Proxy: http.ProxyURL(proxyURL)}, Timeout: 3 * time.Second}
			defer client.CloseIdleConnections()
			resp, err := client.Get("http://interrupted.invalid/")
			if err == nil {
				_, err = io.ReadAll(resp.Body)
				resp.Body.Close()
			}
			if err == nil {
				t.Fatal("incomplete upstream response was reported as complete")
			}
			if e, ok := err.(net.Error); ok && e.Timeout() {
				t.Fatal("broken response hung instead of closing", err)
			}
		})
	}
}

func TestSSEHeadersBeforeFirstEvent(t *testing.T) {
	release := make(chan struct{})
	origin := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream; charset=utf-8")
		w.WriteHeader(http.StatusOK)
		w.(http.Flusher).Flush()
		<-release
		io.WriteString(w, "data: first\n\n")
	}))
	defer func() { close(release); origin.Close() }()
	_, server, _ := testutil.BridgeFixture(t, strings.TrimPrefix(origin.URL, "http://"))
	proxyURL, _ := url.Parse(server.URL)
	client := &http.Client{Transport: &http.Transport{Proxy: http.ProxyURL(proxyURL)}, Timeout: 3 * time.Second}
	defer client.CloseIdleConnections()
	resp, err := client.Get("http://events.invalid/")
	if err != nil {
		t.Fatal("SSE headers must arrive before the first event", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK || !strings.HasPrefix(resp.Header.Get("Content-Type"), "text/event-stream") {
		t.Fatalf("wrong SSE response: %v", resp)
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

func TestConnectHalfClosePreservesResponse(t *testing.T) {
	origin, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer origin.Close()
	go func() {
		c, err := origin.Accept()
		if err != nil {
			return
		}
		defer c.Close()
		c.SetDeadline(time.Now().Add(5 * time.Second))
		request, err := io.ReadAll(c)
		if err == nil {
			c.Write(append([]byte("reply:"), request...))
		}
	}()
	_, server, _ := testutil.BridgeFixture(t, origin.Addr().String())
	c, err := net.Dial("tcp", server.Listener.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	c.SetDeadline(time.Now().Add(5 * time.Second))
	io.WriteString(c, "CONNECT echo.invalid:443 HTTP/1.1\r\nHost: echo.invalid:443\r\n\r\n")
	reader := bufio.NewReader(c)
	response, err := http.ReadResponse(reader, &http.Request{Method: "CONNECT"})
	if err != nil || response.StatusCode != 200 {
		t.Fatalf("CONNECT: %v %v", response, err)
	}
	io.WriteString(c, "request")
	if err := c.(*net.TCPConn).CloseWrite(); err != nil {
		t.Fatal(err)
	}
	got, err := io.ReadAll(reader)
	if err != nil || string(got) != "reply:request" {
		t.Fatalf("half-close lost response: %q %v", got, err)
	}
}

func TestWebSocketClientClosesOnUpstreamExitAndFailover(t *testing.T) {
	for _, failover := range []bool{false, true} {
		t.Run(map[bool]string{false: "upstream-exit", true: "failover"}[failover], func(t *testing.T) {
			release := make(chan struct{})
			origin := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				c, err := websocket.Accept(w, r, nil)
				if err != nil {
					return
				}
				defer c.CloseNow()
				<-release
			}))
			defer origin.Close()
			b, server, _ := testutil.BridgeFixture(t, strings.TrimPrefix(origin.URL, "http://"))
			c, err := net.Dial("tcp", server.Listener.Addr().String())
			if err != nil {
				close(release)
				t.Fatal(err)
			}
			defer c.Close()
			c.SetDeadline(time.Now().Add(5 * time.Second))
			io.WriteString(c, "GET http://socket.invalid/ HTTP/1.1\r\nHost: socket.invalid\r\nConnection: Upgrade\r\nUpgrade: websocket\r\nSec-WebSocket-Version: 13\r\nSec-WebSocket-Key: dGhlIHNhbXBsZSBub25jZQ==\r\n\r\n")
			reader := bufio.NewReader(c)
			resp, err := http.ReadResponse(reader, &http.Request{Method: "GET"})
			if err != nil || resp.StatusCode != 101 {
				close(release)
				t.Fatalf("upgrade: %v %v", resp, err)
			}
			if failover {
				b.SetReady(false)
			} else {
				close(release)
			}
			_, err = reader.ReadByte()
			if failover {
				close(release)
			}
			if err != io.EOF {
				t.Fatalf("client socket did not close: %v", err)
			}
		})
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
	for _, probeTimeout := range []time.Duration{50 * time.Millisecond, 300 * time.Millisecond} {
		t.Run(probeTimeout.String(), func(t *testing.T) {
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
			b, err := bridge.NewBridge(ln.Addr().String(), 100*time.Millisecond)
			if err != nil {
				t.Fatal(err)
			}
			start := time.Now()
			if err = b.Probe(context.Background(), "unresolved.invalid:443", probeTimeout); err == nil {
				t.Fatal("stalled SOCKS accepted")
			}
			if elapsed := time.Since(start); elapsed < probeTimeout || elapsed > 2*time.Second {
				t.Fatalf("probe used the wrong deadline: took %s, expected %s", elapsed, probeTimeout)
			}
		})
	}
}

func TestProbeAllowsOneUnavailableTarget(t *testing.T) {
	origin := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	defer origin.Close()
	address, _ := testutil.FakeSOCKS(t, strings.TrimPrefix(origin.URL, "http://"))
	b, err := bridge.NewBridge(address, time.Second)
	if err != nil {
		t.Fatal(err)
	}
	if err = bridge.ProbeTargets(context.Background(), b, []string{"invalid-address", "working.invalid:443"}, time.Second); err != nil {
		t.Fatal(err)
	}
}
