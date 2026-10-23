//go:build darwin || linux

package tests

import (
	"context"
	"syscall"
	"testing"

	"ssh_proxy/src/control"
	"ssh_proxy/src/supervisor"
)

// SIGTERM (logout, systemctl --user stop) is a requested stop, not a crash.
func TestSignalIsRequestedStop(t *testing.T) {
	p := supervisorPaths(t)
	done := make(chan error, 1)
	go func() { done <- supervisor.RunContext(context.Background(), p, false) }()
	waitSupervisor(t, p)
	if err := syscall.Kill(syscall.Getpid(), syscall.SIGTERM); err != nil {
		t.Fatal(err)
	}
	if err := <-done; err != nil {
		t.Fatal("signal stop reported an error", err)
	}
	if st := control.QueryStatus(p); st.UncleanExit || st.Phase != control.PhaseStopped {
		t.Fatal("signal stop recorded as unexpected", st)
	}
}
