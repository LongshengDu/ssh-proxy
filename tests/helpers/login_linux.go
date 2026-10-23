//go:build systemtest

package testutil

import (
	"os/exec"
	"testing"

	"ssh_proxy/src/platform"
)

func CleanupTestLogins() error { return nil }

func StartRegisteredProxy(t *testing.T, entry platform.LoginEntry) {
	t.Helper()
	t.Cleanup(func() { exec.Command("systemctl", "--user", "stop", entry.Name+".service").Run() })
	if out, err := exec.Command("systemctl", "--user", "start", entry.Name+".service").CombinedOutput(); err != nil {
		t.Fatal(err, string(out))
	}
}
