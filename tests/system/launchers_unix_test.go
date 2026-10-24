//go:build systemtest && (darwin || linux)

package tests

import (
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"testing"

	"ssh-proxy/tests/helpers"
)

// run-via-proxy.sh runs a program with its own arguments and the proxy URL
// that status reports, or the one --proxy-http names.
func TestRunViaProxy(t *testing.T) {
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	dir := testutil.IsolatedBuild(t)
	record := filepath.Join(dir, "launch.json")
	t.Setenv("SSH_PROXY_TEST_PROCESS", "desktop")
	t.Setenv("SSH_PROXY_TEST_RECORD", record)
	type launch struct {
		Args  []string
		Proxy string
	}
	run := func(args ...string) (launch, error) {
		t.Helper()
		os.Remove(record)
		var got launch
		out, err := testutil.Command(t, "/bin/bash", append([]string{filepath.Join(dir, "run-via-proxy.sh")}, args...)...).CombinedOutput()
		if err == nil {
			if err := json.Unmarshal([]byte(testutil.ReadFile(record)), &got); err != nil {
				t.Fatalf("program did not run: %v %s", err, out)
			}
		}
		return got, err
	}
	if _, err := run(executable); err == nil {
		t.Fatal("ran without a running proxy")
	}
	got, err := run("--proxy-http", "http://127.0.0.1:9", executable, "one", "two words")
	if err != nil || got.Proxy != "http://127.0.0.1:9" || !slices.Equal(got.Args, []string{"one", "two words"}) {
		t.Fatalf("override with arguments: %+v, %v", got, err)
	}
	testutil.LocalSSH(t, dir)
	t.Cleanup(func() { testutil.CallCLI(dir, "stop") })
	testutil.MustCLI(t, dir, "start", "--wait", "15")
	if got, err := run(executable); err != nil || got.Proxy != "http://"+testutil.StatusCLI(t, dir).HTTP || len(got.Args) != 0 {
		t.Fatalf("running proxy: %+v, %v", got, err)
	}
}
