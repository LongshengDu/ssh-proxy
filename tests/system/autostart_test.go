//go:build systemtest

package tests

import (
	"strings"
	"testing"

	"ssh-proxy/src/config"
	"ssh-proxy/tests/helpers"
)

func TestProxyAutostart(t *testing.T) {
	t.Parallel()
	// OS launch managers do not inherit the test's environment; the isolated
	// install path alone gives the registered proxy its own runtime folder.
	dir := testutil.IsolatedBuild(t)
	p, err := config.PathsForExecutable(testutil.CLIPath(dir))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { testutil.CallCLI(dir, "stop"); p.AutostartEntry().Disable() })
	if out, err := testutil.CallCLI(dir, "autostart", "enable"); err == nil {
		t.Fatal("empty SSH config accepted", string(out))
	}
	if enabled, _, err := p.AutostartEntry().Status(); err != nil || enabled {
		t.Fatal(enabled, err)
	}
	fixture := testutil.LocalSSH(t, dir)
	testutil.MustCLI(t, dir, "autostart", "enable")
	if st := testutil.StatusCLI(t, dir); !st.Autostart || st.PID != 0 {
		t.Fatal("enable must not start", st)
	}
	testutil.StartRegisteredProxy(t, p.AutostartEntry())
	testutil.WaitFor(t, func() bool { return testutil.StatusCLI(t, dir).Phase == "running" })
	pid := testutil.StatusCLI(t, dir).PID
	testutil.MustCLI(t, dir, "start", "--wait", "1")
	if st := testutil.StatusCLI(t, dir); st.PID != pid {
		t.Fatal("duplicate start replaced proxy", st)
	}
	testutil.MustCLI(t, dir, "autostart", "disable")
	if st := testutil.StatusCLI(t, dir); st.Autostart || st.PID != pid || st.Phase != "running" {
		t.Fatal("disable stopped proxy", st)
	}
	testutil.MustCLI(t, dir, "stop")
	if st := testutil.StatusCLI(t, dir); st.Phase != "stopped" || st.LastError != "" {
		t.Fatal("normal stop reported failure", st)
	}
	fixture.Primary.Close()
	fixture.Backup.Close()
	if out := string(testutil.MustCLI(t, dir, "autostart", "enable")); !strings.Contains(out, "Warning:") || !strings.Contains(out, "enabled") {
		t.Fatal("offline enable must warn and register", out)
	}
	testutil.MustCLI(t, dir, "autostart", "disable")
}
