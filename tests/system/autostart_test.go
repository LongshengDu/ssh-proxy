//go:build systemtest

package tests

import (
	"os"
	"strings"
	"testing"

	"ssh_proxy/src/config"
	"ssh_proxy/tests/helpers"
)

func TestProxyAutostart(t *testing.T) {
	dir := testutil.IsolatedBuild(t)
	// OS launch managers do not inherit the test runner's environment. The
	// installation identity itself isolates this run in the normal cache.
	t.Setenv("SSH_PROXY_RUNTIME", "")
	p, err := config.PathsForExecutable(testutil.CLIPath(dir))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { testutil.CallCLI(dir, "stop"); p.AutostartEntry().Disable(); os.RemoveAll(p.Runtime) })
	for _, args := range [][]string{{"service"}, {"service", "install"}, {"service", "start"}, {"_service"}} {
		if out, err := testutil.CallCLI(dir, args...); err == nil || !strings.Contains(string(out), "unknown command") {
			t.Fatal(args, err, string(out))
		}
	}
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
