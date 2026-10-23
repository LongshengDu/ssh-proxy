//go:build systemtest

package tests

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"ssh-proxy/src/platform"
	"ssh-proxy/tests/helpers"
)

// The guided login sets up a new server end to end: it creates the configured
// key, confirms the host, logs in once with the password, installs the key and
// then logs in without prompts. OpenSSH's askpass answers the prompts, so no
// terminal is needed on any OS, and only this test's folders are written.
func TestCLIGuidedLogin(t *testing.T) {
	t.Parallel()
	helper, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	dir := testutil.IsolatedBuild(t)
	f := testutil.LocalSSH(t, dir)
	// A configured key that doesn't exist yet, with one of OpenSSH's default names.
	key := filepath.Join(dir, "keys", "id_rsa")
	quoted := func(path string) string { return strconv.Quote(filepath.ToSlash(path)) }
	testutil.WriteFile(t, f.Trust, "") // The host is not trusted yet.
	testutil.WriteFile(t, filepath.Join(dir, "ssh_config"), fmt.Sprintf("Host primary\n HostName 127.0.0.1\n Port %d\n User tester\n"+
		" IdentityFile %s\n IdentitiesOnly yes\n IdentityAgent none\n UserKnownHostsFile %s\n GlobalKnownHostsFile none\n",
		f.PrimaryPort, quoted(key), quoted(f.Trust)))
	t.Cleanup(func() { testutil.CallCLI(dir, "stop") })
	login := func() string {
		t.Helper()
		ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
		defer cancel()
		cmd := exec.CommandContext(ctx, testutil.CLIPath(dir), "login")
		cmd.Env = append(os.Environ(), "SSH_ASKPASS="+helper, "SSH_ASKPASS_REQUIRE=force",
			"SSH_PROXY_TEST_PROCESS=askpass", "SSH_PROXY_TEST_PASSWORD="+testutil.FixturePassword)
		platform.HideChild(cmd)
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("login: %v: %s", err, out)
		}
		return string(out)
	}
	if out := login(); !strings.Contains(out, "Created SSH key") || !strings.Contains(out, "Ready: primary") {
		t.Fatal("guided login did not finish setup:", out)
	}
	for _, path := range []string{key, key + ".pub", f.Trust} {
		if testutil.ReadFile(path) == "" {
			t.Fatal("setup did not write", path)
		}
	}
	if f.Installed() != 1 {
		t.Fatalf("the server received %d keys, want 1", f.Installed())
	}
	// Finished steps are skipped: no new key and no second install.
	if out := login(); !strings.Contains(out, "already logs in without prompts") || f.Installed() != 1 {
		t.Fatal("a working login repeated setup:", out)
	}
	testutil.MustCLI(t, dir, "login", "--check")
	testutil.MustCLI(t, dir, "start", "--wait", "15")
	testutil.MustCLI(t, dir, "test", "--url", f.Origin.URL)
}
