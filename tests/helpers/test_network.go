package testutil

import (
	"encoding/binary"
	"io"
	"net"
	"net/http/httptest"
	"strconv"
	"sync"
	"testing"
	"time"

	"ssh_proxy/src/bridge"
)

// A local SOCKS fixture records the requested hostname and connects to a test
// origin. This checks remote DNS, framing, and the absence of direct fallback.
func FakeSOCKS(t *testing.T, origin string) (string, <-chan string) {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	targets := make(chan string, 16)
	var mu sync.Mutex
	active := map[net.Conn]bool{}
	t.Cleanup(func() {
		listener.Close()
		mu.Lock()
		defer mu.Unlock()
		for conn := range active {
			conn.Close()
		}
	})
	go func() {
		for {
			client, err := listener.Accept()
			if err != nil {
				return
			}
			mu.Lock()
			active[client] = true
			mu.Unlock()
			go func() {
				defer client.Close()
				defer func() { mu.Lock(); delete(active, client); mu.Unlock() }()
				client.SetDeadline(time.Now().Add(10 * time.Second))
				header := make([]byte, 2)
				if _, err := io.ReadFull(client, header); err != nil {
					return
				}
				if header[0] != 5 {
					return
				}
				methods := make([]byte, int(header[1]))
				if _, err := io.ReadFull(client, methods); err != nil {
					return
				}
				client.Write([]byte{5, 0})
				req := make([]byte, 4)
				if _, err := io.ReadFull(client, req); err != nil {
					return
				}
				var host string
				switch req[3] {
				case 3:
					size := make([]byte, 1)
					if _, err := io.ReadFull(client, size); err != nil {
						return
					}
					raw := make([]byte, int(size[0]))
					if _, err := io.ReadFull(client, raw); err != nil {
						return
					}
					host = string(raw)
				case 1:
					raw := make([]byte, 4)
					if _, err := io.ReadFull(client, raw); err != nil {
						return
					}
					host = net.IP(raw).String()
				case 4:
					raw := make([]byte, 16)
					if _, err := io.ReadFull(client, raw); err != nil {
						return
					}
					host = net.IP(raw).String()
				default:
					return
				}
				port := make([]byte, 2)
				if _, err := io.ReadFull(client, port); err != nil {
					return
				}
				target := net.JoinHostPort(host, strconv.Itoa(int(binary.BigEndian.Uint16(port))))
				targets <- target
				remote, err := net.DialTimeout("tcp", origin, time.Second)
				if err != nil {
					return
				}
				defer remote.Close()
				client.Write([]byte{5, 0, 0, 1, 127, 0, 0, 1, 0, 0})
				done := make(chan struct{})
				go func() { io.Copy(remote, client); remote.Close(); close(done) }()
				io.Copy(client, remote)
				client.Close()
				<-done
			}()
		}
	}()
	return listener.Addr().String(), targets
}

func BridgeFixture(t *testing.T, origin string) (*bridge.Bridge, *httptest.Server, <-chan string) {
	t.Helper()
	socks, targets := FakeSOCKS(t, origin)
	b, err := bridge.NewBridge(socks, time.Second)
	if err != nil {
		t.Fatal(err)
	}
	b.SetReady(true)
	server := httptest.NewServer(b)
	t.Cleanup(func() { b.SetReady(false); server.Close() })
	return b, server, targets
}
