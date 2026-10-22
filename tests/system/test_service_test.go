//go:build systemtest

package tests

import (
	"os/user"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"ssh_proxy/tests/helpers"
)

func TestServiceCommands(t *testing.T) {
	dir := testutil.IsolatedBuild(t)
	// This random config identity has no OS registration. No credentials,
	// elevation, real account settings or existing services are modified.
	for _, action := range []string{"status", "uninstall"} {
		out := string(testutil.MustCLI(t, dir, "service", action))
		if !strings.HasPrefix(out, "Service com.sshproxy.") || !strings.Contains(out, "is not installed") {
			t.Fatalf("unregistered service %s: %s", action, out)
		}
	}
	for _, action := range []string{"enable", "disable", "invalid", "help"} {
		if out, err := testutil.CallCLI(dir, "service", action); err == nil {
			t.Fatalf("unregistered/invalid service %s succeeded: %s", action, out)
		}
	}
	for _, action := range []string{"start", "stop", "restart"} {
		if out, err := testutil.CallCLI(dir, "service", action); err == nil || !strings.Contains(string(out), "use "+action+" directly") {
			t.Fatalf("duplicate service control %s: %v %s", action, err, out)
		}
	}
	for _, args := range [][]string{{"--user", "other"}, {"--user=other"}} {
		out, err := testutil.CallCLI(dir, append([]string{"service", "install"}, args...)...)
		if err == nil || !strings.Contains(string(out), "flag provided but not defined: -user") {
			t.Fatalf("account selection must be rejected before installation: %v %s", err, out)
		}
	}
	if runtime.GOOS == "windows" {
		current, err := user.Current()
		if err != nil {
			t.Fatal(err)
		}
		testutil.WriteFile(t, filepath.Join(dir, "ssh_config"), "Host test-proxy\n HostName example.invalid\n User tester\n")
		// With no terminal input, installation must detect our account and stop
		// before requesting credentials or changing OS registration.
		out, err := testutil.CallCLI(dir, "service", "install")
		if err == nil || !strings.Contains(string(out), "requires an interactive terminal") || !strings.Contains(string(out), current.Username) {
			t.Fatalf("installation must infer the current account: %v %s", err, out)
		}
	}
}
