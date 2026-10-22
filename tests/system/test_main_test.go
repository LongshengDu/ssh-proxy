//go:build systemtest

package tests

import (
	"os"
	"runtime"
	"testing"

	"ssh_proxy/src/platform"
	"ssh_proxy/tests/helpers"
)

func TestMain(m *testing.M) {
	if testutil.RunProcess() {
		return
	}
	if os.Getenv("SSH_PROXY_SYS_DIR") == "" {
		println("Run python scripts/run_sys_tests.py against a native development build.")
		os.Exit(1)
	}
	var unlock func()
	if runtime.GOOS != "linux" && os.Getenv("SSH_PROXY_SYS_SCOPE") != "cli" {
		var err error
		unlock, err = platform.LockInstance("system-tests-desktop")
		if err != nil {
			println("Another native desktop test suite is running; wait for it to finish.")
			os.Exit(1)
		}
	}
	code := m.Run()
	if unlock != nil {
		unlock()
	}
	os.Exit(code)
}
