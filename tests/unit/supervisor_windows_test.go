package tests

import (
	"os"
	"os/exec"
	"path/filepath"
	"syscall"
	"testing"
	"time"

	"golang.org/x/sys/windows"

	"ssh-proxy/src/control"
)

// A test supervisor presses Ctrl+Break in its own console once this file exists.
func init() {
	trigger := os.Getenv("SSH_PROXY_TEST_BREAK_FILE")
	if trigger == "" || len(os.Args) < 2 || os.Args[1] != "_run" {
		return
	}
	go func() {
		for {
			if _, err := os.Stat(trigger); err == nil {
				windows.GenerateConsoleCtrlEvent(windows.CTRL_BREAK_EVENT, 0)
				return
			}
			time.Sleep(10 * time.Millisecond)
		}
	}()
}

// Ctrl+C or Ctrl+Break in a foreground console is a requested stop, not a
// crash, as TestSignalIsRequestedStop checks for Unix signals.
func TestConsoleBreakIsRequestedStop(t *testing.T) {
	p := supervisorPaths(t)
	trigger := filepath.Join(t.TempDir(), "break")
	t.Setenv("SSH_PROXY_TEST_BREAK_FILE", trigger)
	run := exec.Command(os.Args[0], "_run")
	// A hidden console of its own, so the event reaches only that supervisor.
	run.SysProcAttr = &syscall.SysProcAttr{HideWindow: true, CreationFlags: windows.CREATE_NEW_CONSOLE}
	if err := run.Start(); err != nil {
		t.Fatal(err)
	}
	waitSupervisor(t, p)
	if err := os.WriteFile(trigger, nil, 0600); err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() { done <- run.Wait() }()
	select {
	case err := <-done:
		if err != nil {
			t.Fatal("console stop reported an error:", err)
		}
	case <-time.After(10 * time.Second):
		run.Process.Kill()
		t.Fatal("supervisor ignored Ctrl+Break")
	}
	if st := control.QueryStatus(p); st.UncleanExit || st.Phase != control.PhaseStopped {
		t.Fatal("console stop recorded as unexpected", st)
	}
}
