//go:build systemtest

package tests

import (
	"context"
	"encoding/json"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"ssh_proxy/src/control"
	"ssh_proxy/src/platform"
	"ssh_proxy/tests/helpers"
)

// The last line native SSH prints reaches status, so users see why it failed.
func TestSSHChildDiagnostics(t *testing.T) {
	t.Parallel()
	helper, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	const diagnostic = "tester@host: Permission denied (publickey,password)."
	dir := testutil.IsolatedBuild(t)
	port := func() string {
		ln, err := net.Listen("tcp", "127.0.0.1:0")
		if err != nil {
			t.Fatal(err)
		}
		defer ln.Close()
		return strconv.Itoa(ln.Addr().(*net.TCPAddr).Port)
	}
	socks, http := port(), port()
	for socks == http {
		http = port()
	}
	text := strings.NewReplacer(`executable = "ssh"`, "executable = "+strconv.Quote(helper),
		"socks_port = 1081", "socks_port = "+socks, "http_port = 8181", "http_port = "+http).Replace(string(testutil.ConfigTemplate(t)))
	testutil.WriteFile(t, filepath.Join(dir, "config.toml"), text)
	testutil.WriteFile(t, filepath.Join(dir, "ssh_config"), "Host needs-trust\n HostName fixture.invalid\n User tester\n")
	env := append(os.Environ(), "SSH_PROXY_TEST_PROCESS=ssh", "SSH_PROXY_TEST_DIAGNOSTIC="+diagnostic)
	call := func(args ...string) ([]byte, error) {
		ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
		defer cancel()
		cmd := exec.CommandContext(ctx, testutil.CLIPath(dir), args...)
		cmd.Env = env
		platform.HideChild(cmd)
		return cmd.CombinedOutput()
	}
	t.Cleanup(func() {
		if out, err := call("stop"); err != nil {
			t.Errorf("cleanup: %v: %s", err, out)
		}
	})
	if out, err := call("start", "--wait", "1"); err != nil {
		t.Fatalf("start: %v: %s", err, out)
	}
	var st control.Status
	testutil.WaitFor(t, func() bool {
		out, err := call("status")
		return err == nil && json.Unmarshal(out, &st) == nil && strings.Contains(st.LastError, diagnostic)
	})
}
