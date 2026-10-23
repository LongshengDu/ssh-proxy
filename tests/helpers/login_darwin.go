//go:build systemtest

package testutil

import (
	"fmt"
	"os"
	"os/exec"
	"testing"

	"ssh_proxy/src/platform"
)

func CleanupTestLogins() error { return nil }

func StartRegisteredProxy(t *testing.T, entry platform.LoginEntry) {
	t.Helper()
	domain := fmt.Sprintf("gui/%d", os.Getuid())
	path, err := entry.Path()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { exec.Command("launchctl", "bootout", domain+"/"+entry.Name).Run() })
	if out, err := exec.Command("launchctl", "bootstrap", domain, path).CombinedOutput(); err != nil {
		t.Fatal(err, string(out))
	}
}
