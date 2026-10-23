//go:build systemtest

package tests

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"
	"unsafe"

	"golang.org/x/sys/windows"

	"ssh-proxy/src/platform"
	"ssh-proxy/tests/helpers"
)

// A logged login session leaves native SSH's input/output on real console
// handles, where OpenSSH's own messages stay; the log gets login's own lines.
// A login that fails in a console of its own waits for Enter before it closes.
func TestTrustLoggingPreservesConsole(t *testing.T) {
	config, _ := testutil.Config(t, "Host first target\n HostName fixture.invalid\n User tester\n")
	data, err := os.ReadFile(testutil.CLIPath(testutil.SystemBuild(t)))
	if err != nil {
		t.Fatal(err)
	}
	cli := testutil.CLIPath(filepath.Dir(config))
	if err := os.WriteFile(cli, data, 0755); err != nil {
		t.Fatal(err)
	}
	helper, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	text := strings.ReplaceAll(string(testutil.ConfigTemplate(t)), `executable = "ssh"`, "executable = "+strconv.Quote(helper))
	if err := os.WriteFile(config, []byte(text), 0600); err != nil {
		t.Fatal(err)
	}
	record, log := filepath.Join(t.TempDir(), "terminal.json"), filepath.Join(t.TempDir(), "trust.txt")
	t.Setenv("SSH_PROXY_TEST_PROCESS", "ssh")
	t.Setenv("SSH_PROXY_TEST_SSH_SUCCESS", "target")
	t.Setenv("SSH_PROXY_TEST_TERMINAL_RECORD", record)
	t.Setenv("USERPROFILE", t.TempDir()) // A failed setup looks for keys; never use the developer's.
	login := func(args ...string) windows.ProcessInformation {
		t.Helper()
		line, err := windows.UTF16PtrFromString(windows.ComposeCommandLine(append([]string{cli, "login", "--log", log}, args...)))
		if err != nil {
			t.Fatal(err)
		}
		startup := windows.StartupInfo{Flags: windows.STARTF_USESHOWWINDOW, ShowWindow: windows.SW_HIDE}
		startup.Cb = uint32(unsafe.Sizeof(startup))
		var process windows.ProcessInformation
		if err := windows.CreateProcess(nil, line, nil, nil, false, windows.CREATE_NEW_CONSOLE, nil, nil, &startup, &process); err != nil {
			t.Fatal(err)
		}
		windows.CloseHandle(process.Thread)
		t.Cleanup(func() { windows.TerminateProcess(process.Process, 1); windows.CloseHandle(process.Process) })
		return process
	}
	exitCode := func(process windows.Handle) uint32 {
		t.Helper()
		state, err := windows.WaitForSingleObject(process, 15000)
		if err != nil || state != windows.WAIT_OBJECT_0 {
			t.Fatalf("login did not finish: %d: %v", state, err)
		}
		var code uint32
		if err := windows.GetExitCodeProcess(process, &code); err != nil {
			t.Fatal(err)
		}
		return code
	}
	if code := exitCode(login().Process); code != 0 {
		t.Fatalf("login failed: %d", code)
	}
	data, err = os.ReadFile(record)
	var terminal []bool
	if err != nil || json.Unmarshal(data, &terminal) != nil || len(terminal) != 3 || !terminal[0] || !terminal[1] || !terminal[2] {
		t.Fatalf("SSH must receive console stdin/stdout/stderr with --log: %s (%v)", data, err)
	}
	data, err = os.ReadFile(log)
	if err != nil || !strings.Contains(string(data), "SSH login for target") || strings.Contains(string(data), "fixture SSH diagnostic") {
		t.Fatalf("the log must get login's lines and the console OpenSSH's: %s (%v)", data, err)
	}
	failed := login("first")
	testutil.WaitFor(t, func() bool {
		return strings.Contains(testutil.ConsoleText(failed.ProcessId), "Press Enter to close this window.")
	})
	testutil.ConsoleType(t, failed.ProcessId, "\r")
	if code := exitCode(failed.Process); code != 1 {
		t.Fatalf("failed login exited with %d, want 1", code)
	}
}

// Terminals, IDEs and CI agents may run commands in a kill-on-close job. A
// background start must leave that job so closing the host keeps the proxy.
func TestCLIStartLeavesCallerJob(t *testing.T) {
	t.Parallel()
	dir := testutil.IsolatedBuild(t)
	testutil.LocalSSH(t, dir)
	t.Cleanup(func() { testutil.CallCLI(dir, "stop") })
	job, err := windows.CreateJobObject(nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	closed := false
	defer func() {
		if !closed {
			windows.CloseHandle(job)
		}
	}()
	info := windows.JOBOBJECT_EXTENDED_LIMIT_INFORMATION{}
	info.BasicLimitInformation.LimitFlags = windows.JOB_OBJECT_LIMIT_KILL_ON_JOB_CLOSE | windows.JOB_OBJECT_LIMIT_BREAKAWAY_OK
	if _, err := windows.SetInformationJobObject(job, windows.JobObjectExtendedLimitInformation, uintptr(unsafe.Pointer(&info)), uint32(unsafe.Sizeof(info))); err != nil {
		t.Fatal(err)
	}
	line, err := windows.UTF16PtrFromString(windows.ComposeCommandLine([]string{testutil.CLIPath(dir), "start", "--wait", "15"}))
	if err != nil {
		t.Fatal(err)
	}
	startup := windows.StartupInfo{}
	startup.Cb = uint32(unsafe.Sizeof(startup))
	var process windows.ProcessInformation
	// Join the job before the CLI runs, so its supervisor starts inside it.
	if err := windows.CreateProcess(nil, line, nil, nil, false, windows.CREATE_SUSPENDED|windows.CREATE_NO_WINDOW, nil, nil, &startup, &process); err != nil {
		t.Fatal(err)
	}
	defer windows.CloseHandle(process.Process)
	if err := windows.AssignProcessToJobObject(job, process.Process); err != nil {
		windows.TerminateProcess(process.Process, 1)
		windows.CloseHandle(process.Thread)
		t.Fatal(err)
	}
	if _, err := windows.ResumeThread(process.Thread); err != nil {
		t.Fatal(err)
	}
	windows.CloseHandle(process.Thread)
	state, err := windows.WaitForSingleObject(process.Process, 30000)
	if err != nil || state != windows.WAIT_OBJECT_0 {
		t.Fatalf("start did not finish: %d: %v", state, err)
	}
	var code uint32
	if err := windows.GetExitCodeProcess(process.Process, &code); err != nil || code != 0 {
		t.Fatalf("start failed: %d: %v", code, err)
	}
	before := testutil.StatusCLI(t, dir)
	if before.Phase != "running" {
		t.Fatalf("not ready: %+v", before)
	}
	windows.CloseHandle(job) // Kills every process still in the job.
	closed = true
	time.Sleep(time.Second)
	after := testutil.StatusCLI(t, dir)
	if after.Phase != "running" || after.PID != before.PID || !platform.ProcessAlive(before.SSHPID) {
		t.Fatalf("closing the caller's job stopped the proxy: before %+v, after %+v", before, after)
	}
}
