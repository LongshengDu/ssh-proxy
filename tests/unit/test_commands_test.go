package tests

import (
	"strings"
	"testing"

	"ssh_proxy/src/proxy"
)

func TestDuplicateControlsRejectedBeforeOSAccess(t *testing.T) {
	// Removed controls must be rejected before touching OS registration.
	t.Setenv("SSH_PROXY_RUNTIME", t.TempDir())
	for _, command := range []string{"start", "stop", "restart"} {
		err := proxy.Run([]string{"service", command})
		if err == nil || !strings.Contains(err.Error(), "use "+command+" directly") {
			t.Fatalf("service %s was not rejected: %v", command, err)
		}
	}
}
