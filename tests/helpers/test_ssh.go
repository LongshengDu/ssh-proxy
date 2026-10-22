//go:build systemtest

package testutil

import (
	"crypto/ed25519"
	"crypto/rand"
	"encoding/pem"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os/exec"
	"os/user"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"

	sshserver "github.com/gliderlabs/ssh"
	ssh "golang.org/x/crypto/ssh"
)

// Reuse an SSH server library in tests; the programs still run native OpenSSH.
// Only ephemeral loopback servers, keys and known_hosts are used.
type SSHFixture struct {
	Primary, Backup      *sshserver.Server
	Blocked              atomic.Bool
	Origin               *httptest.Server
	Config, Hosts, Trust string
	Fingerprint          string
}

func FreePort(t *testing.T) int {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	return ln.Addr().(*net.TCPAddr).Port
}
func LocalSSH(t *testing.T, dir string) *SSHFixture {
	t.Helper()
	f := &SSHFixture{}
	_, private, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	signer, err := ssh.NewSignerFromKey(private)
	if err != nil {
		t.Fatal(err)
	}
	f.Fingerprint = ssh.FingerprintSHA256(signer.PublicKey())
	block, err := ssh.MarshalPrivateKey(private, "")
	if err != nil {
		t.Fatal(err)
	}
	key := filepath.Join(dir, "test_key")
	WriteFile(t, key, string(pem.EncodeToMemory(block)))
	if runtime.GOOS == "windows" {
		account, err := user.Current()
		if err != nil {
			t.Fatal(err)
		}
		// Windows ignores Unix mode bits. Restrict only this generated fixture key.
		if out, err := exec.Command("icacls.exe", key, "/inheritance:r", "/grant:r", "*"+account.Uid+":R").CombinedOutput(); err != nil {
			t.Fatalf("protect temporary SSH key: %v: %s", err, out)
		}
	}
	f.Origin = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/plain")
		io.WriteString(w, "through native SSH\n")
	}))
	t.Cleanup(f.Origin.Close)
	serve := func(primary bool) (*sshserver.Server, int) {
		ln, err := net.Listen("tcp", "127.0.0.1:0")
		if err != nil {
			t.Fatal(err)
		}
		srv := &sshserver.Server{
			HostSigners: []sshserver.Signer{signer},
			PublicKeyHandler: func(_ sshserver.Context, key sshserver.PublicKey) bool {
				return sshserver.KeysEqual(key, signer.PublicKey())
			},
			Handler:         func(session sshserver.Session) { session.Exit(0) },
			ChannelHandlers: map[string]sshserver.ChannelHandler{"session": sshserver.DefaultSessionHandler, "direct-tcpip": sshserver.DirectTCPIPHandler},
			LocalPortForwardingCallback: func(_ sshserver.Context, host string, port uint32) bool {
				return host == "127.0.0.1" && (!primary || !f.Blocked.Load())
			},
		}
		go srv.Serve(ln)
		t.Cleanup(func() { srv.Close() })
		return srv, ln.Addr().(*net.TCPAddr).Port
	}
	var first, second int
	f.Primary, first = serve(true)
	f.Backup, second = serve(false)
	f.Trust = filepath.Join(dir, "known_hosts")
	for _, port := range []int{first, second} {
		f.Hosts += fmt.Sprintf("[127.0.0.1]:%d %s", port, ssh.MarshalAuthorizedKey(signer.PublicKey()))
	}
	WriteFile(t, f.Trust, f.Hosts)
	quoted := func(path string) string { return strconv.Quote(filepath.ToSlash(path)) }
	WriteFile(t, filepath.Join(dir, "ssh_config"), fmt.Sprintf(`Host unavailable
 HostName 127.0.0.1
 Port %d
Host primary
 HostName 127.0.0.1
 Port %d
Host backup
 HostName 127.0.0.1
 Port %d
Host *
 User tester
 IdentityFile %s
 IdentitiesOnly yes
 IdentityAgent none
 UserKnownHostsFile %s
 GlobalKnownHostsFile none
`, FreePort(t), first, second, quoted(key), quoted(f.Trust)))
	socks, httpPort := FreePort(t), FreePort(t)
	for socks == httpPort {
		httpPort = FreePort(t)
	}
	f.Config = fmt.Sprintf(`[proxy]
bind = "127.0.0.1"
socks_port = %d
http_port = %d
[ssh]
executable = "ssh"
config_file = "ssh_config"
connect_timeout_seconds = 1
connection_attempts = 1
server_alive_interval_seconds = 1
server_alive_count_max = 2
ready_timeout_seconds = 3
retry_delay_seconds = 1
[health]
targets = [%q]
interval_seconds = 1
timeout_seconds = 1
failures_before_failover = 2
failover_on_probe_failure = false
`, socks, httpPort, strings.TrimPrefix(f.Origin.URL, "http://"))
	WriteFile(t, filepath.Join(dir, "config.toml"), f.Config)
	return f
}
