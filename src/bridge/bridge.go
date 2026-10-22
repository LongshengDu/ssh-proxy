package bridge

import (
	"bufio"
	"context"
	"fmt"
	"github.com/elazarl/goproxy"
	"golang.org/x/net/proxy"
	"io"
	"log"
	"net"
	"net/http"
	"strings"
	"sync"
	"time"
)

// Bridge delegates HTTP, CONNECT, WebSockets and streaming to goproxy.
// All upstream sockets use SOCKS5 with remote DNS; no direct fallback.
type Bridge struct {
	proxy       *goproxy.ProxyHttpServer
	dial        func(context.Context, string, string) (net.Conn, error)
	mu          sync.Mutex
	ready       bool
	generation  uint64
	connections map[*trackedConn]struct{}
}

// SOCKSDialer dials through the SOCKS5 listener with remote DNS and a per-dial timeout.
func SOCKSDialer(address string, timeout time.Duration) (func(context.Context, string, string) (net.Conn, error), error) {
	d, err := proxy.SOCKS5("tcp", address, nil, &net.Dialer{Timeout: timeout, KeepAlive: 30 * time.Second})
	if err != nil {
		return nil, err
	}
	cd, ok := d.(proxy.ContextDialer)
	if !ok {
		return nil, fmt.Errorf("SOCKS dialer does not support cancellation")
	}
	return func(ctx context.Context, network, addr string) (net.Conn, error) {
		ctx, cancel := context.WithTimeout(ctx, timeout)
		defer cancel()
		return cd.DialContext(ctx, network, addr)
	}, nil
}
func NewBridge(address string, timeout time.Duration) (*Bridge, error) {
	dial, err := SOCKSDialer(address, timeout)
	if err != nil {
		return nil, err
	}
	b := &Bridge{dial: dial, connections: make(map[*trackedConn]struct{}), proxy: goproxy.NewProxyHttpServer()}
	b.proxy.Logger = log.New(io.Discard, "", 0)
	b.proxy.Tr = &http.Transport{Proxy: nil, DialContext: b.dialTracked,
		TLSHandshakeTimeout: 15 * time.Second, IdleConnTimeout: 90 * time.Second,
		MaxIdleConns: 100, MaxIdleConnsPerHost: 10}
	// Override the environment-derived CONNECT proxy dialer as well.
	b.proxy.ConnectDial = nil
	b.proxy.ConnectDialWithReq = func(r *http.Request, network, addr string) (net.Conn, error) {
		return b.dialTracked(r.Context(), network, addr)
	}
	b.proxy.OnRequest().DoFunc(func(r *http.Request, ctx *goproxy.ProxyCtx) (*http.Request, *http.Response) {
		removeHopHeaders(r.Header)
		return r, nil
	})
	b.proxy.OnResponse().DoFunc(func(r *http.Response, ctx *goproxy.ProxyCtx) *http.Response {
		if r == nil {
			return goproxy.NewResponse(ctx.Req, goproxy.ContentTypeText, http.StatusBadGateway, "Upstream SSH proxy connection failed")
		}
		removeHopHeaders(r.Header)
		return r
	})
	return b, nil
}

// Remove RFC Connection-nominated headers while preserving WebSocket upgrades.
func removeHopHeaders(h http.Header) {
	upgrade := h.Get("Upgrade") != ""
	for _, value := range h.Values("Connection") {
		for _, name := range strings.Split(value, ",") {
			name = strings.TrimSpace(name)
			if !upgrade || !strings.EqualFold(name, "upgrade") {
				h.Del(name)
			}
		}
	}
	for _, name := range []string{"Proxy-Connection", "Keep-Alive", "Proxy-Authenticate", "Proxy-Authorization", "TE"} {
		h.Del(name)
	}
	if upgrade {
		h.Set("Connection", "Upgrade")
	} else {
		h.Del("Connection")
	}
}
func (b *Bridge) Probe(ctx context.Context, target string, timeout time.Duration) error {
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	c, err := b.dial(ctx, "tcp", target)
	if err == nil {
		c.Close()
	}
	return err
}
func (b *Bridge) dialTracked(ctx context.Context, network, addr string) (net.Conn, error) {
	b.mu.Lock()
	ready, generation := b.ready, b.generation
	b.mu.Unlock()
	if !ready {
		return nil, fmt.Errorf("SSH tunnel unavailable")
	}
	c, err := b.dial(ctx, network, addr)
	if err != nil {
		return nil, err
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	if !b.ready || generation != b.generation {
		c.Close()
		return nil, fmt.Errorf("SSH tunnel changed during connection")
	}
	t := &trackedConn{Conn: c, bridge: b}
	b.connections[t] = struct{}{}
	return t, nil
}

type trackedConn struct {
	net.Conn
	bridge *Bridge
	reader io.Reader
}

func (c *trackedConn) Read(p []byte) (int, error) {
	if c.reader != nil {
		return c.reader.Read(p)
	}
	return c.Conn.Read(p)
}
func (c *trackedConn) Close() error {
	c.bridge.mu.Lock()
	delete(c.bridge.connections, c)
	c.bridge.mu.Unlock()
	return c.Conn.Close()
}
func (c *trackedConn) CloseWrite() error {
	if v, ok := c.Conn.(interface{ CloseWrite() error }); ok {
		return v.CloseWrite()
	}
	return c.Close()
}
func (c *trackedConn) CloseRead() error {
	if v, ok := c.Conn.(interface{ CloseRead() error }); ok {
		return v.CloseRead()
	}
	return c.Close()
}
func (b *Bridge) SetReady(ready bool) {
	b.mu.Lock()
	b.ready = ready
	var closing []*trackedConn
	if !ready {
		b.generation++
		for c := range b.connections {
			closing = append(closing, c)
		}
	}
	b.mu.Unlock()
	for _, c := range closing {
		c.Close()
	}
	if !ready {
		b.proxy.Tr.CloseIdleConnections()
	}
}
func (b *Bridge) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	b.mu.Lock()
	ready := b.ready
	b.mu.Unlock()
	if !ready {
		http.Error(w, "SSH tunnel is connecting or unavailable", http.StatusServiceUnavailable)
		return
	}
	if r.Method != http.MethodConnect && (r.URL.Host == "" || (r.URL.Scheme != "http" && r.URL.Scheme != "https")) {
		http.Error(w, "Use an absolute HTTP URL or CONNECT", http.StatusBadRequest)
		return
	}
	if r.Method == http.MethodConnect {
		b.proxy.ServeHTTP(connectWriter{w, b}, r)
		return
	}
	b.proxy.ServeHTTP(streamWriter{w}, r)
}

// Flush writes without imposing a lifetime timeout on SSE or chunked streams.
// net/http still owns framing and trailers; goproxy owns body forwarding.
type streamWriter struct{ http.ResponseWriter }

func (w streamWriter) Write(p []byte) (int, error) {
	n, err := w.ResponseWriter.Write(p)
	if err == nil {
		err = http.NewResponseController(w.ResponseWriter).Flush()
	}
	return n, err
}
func (w streamWriter) Flush() { _ = http.NewResponseController(w.ResponseWriter).Flush() }
func (w streamWriter) Hijack() (net.Conn, *bufio.ReadWriter, error) {
	return http.NewResponseController(w.ResponseWriter).Hijack()
}

// goproxy's CONNECT handler ignores Hijack's buffered reader. Hand it a Conn
// that consumes those already-read bytes first, retaining TCP half-close.
type connectWriter struct {
	http.ResponseWriter
	bridge *Bridge
}

func (w connectWriter) Hijack() (net.Conn, *bufio.ReadWriter, error) {
	c, rw, err := http.NewResponseController(w.ResponseWriter).Hijack()
	if err != nil {
		return nil, nil, err
	}
	w.bridge.mu.Lock()
	defer w.bridge.mu.Unlock()
	if !w.bridge.ready {
		c.Close()
		return nil, nil, fmt.Errorf("SSH tunnel unavailable")
	}
	tracked := &trackedConn{Conn: c, bridge: w.bridge, reader: rw.Reader}
	w.bridge.connections[tracked] = struct{}{}
	return tracked, rw, nil
}

// ProbeTargets requires one independently reachable destination, not all of them.
func ProbeTargets(ctx context.Context, bridge *Bridge, targets []string, timeout time.Duration) error {
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	results := make(chan error, len(targets))
	for _, target := range targets {
		go func(target string) { results <- bridge.Probe(ctx, target, timeout) }(target)
	}
	var last error
	for range targets {
		select {
		case err := <-results:
			if err == nil {
				return nil
			}
			last = err
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	return fmt.Errorf("all forwarding probes failed: %v", last)
}
