//go:build systemtest && (darwin || linux)

package tests

import (
	"os"
	"os/exec"
	"path/filepath"
	"syscall"
	"testing"

	"ssh-proxy/src/platform"
	"ssh-proxy/tests/helpers"
)

// Terminals, IDEs and agents end a command by killing its process group. A
// background start must leave that group, as TestCLIStartLeavesCallerJob checks
// for Windows jobs.
func TestCLIStartLeavesCallerSession(t *testing.T) {
	t.Parallel()
	dir := testutil.IsolatedBuild(t)
	testutil.LocalSSH(t, dir)
	t.Cleanup(func() { testutil.CallCLI(dir, "stop") })
	started := filepath.Join(dir, "started")
	shell := exec.Command("/bin/sh", "-c", `"$0" start --wait 15 && touch "$1"; sleep 60`, testutil.CLIPath(dir), started)
	shell.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	if err := shell.Start(); err != nil {
		t.Fatal(err)
	}
	testutil.WaitFor(t, func() bool { _, err := os.Stat(started); return err == nil })
	before := testutil.StatusCLI(t, dir)
	if group, err := syscall.Getpgid(before.PID); err != nil || group == shell.Process.Pid {
		t.Fatalf("the proxy stayed in the caller's process group: %d %v", group, err)
	}
	syscall.Kill(-shell.Process.Pid, syscall.SIGHUP)
	syscall.Kill(-shell.Process.Pid, syscall.SIGKILL)
	shell.Wait()
	after := testutil.StatusCLI(t, dir)
	if after.Phase != "running" || after.PID != before.PID || !platform.ProcessAlive(before.SSHPID) {
		t.Fatalf("ending the caller's process group stopped the proxy: before %+v, after %+v", before, after)
	}
}
