//go:build darwin || linux

package platform

import (
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"os/signal"
	"syscall"
)

// RunGuardian is the _ssh helper used on macOS and Linux. EOF means the owning supervisor exited.
// SSH runs in its own process group, so ending it also ends a ProxyCommand or
// ProxyJump process it started.
func RunGuardian(args []string) error {
	if len(args) == 0 {
		return fmt.Errorf("missing SSH executable")
	}
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()
	go func() { io.Copy(io.Discard, os.Stdin); cancel() }()
	// Go raises its own open-file limit but gives children the caller's, 256 on
	// macOS, and each proxied connection holds one ssh file. 1024 stays below
	// FD_SETSIZE for OpenSSH releases that still use select.
	var files syscall.Rlimit
	if syscall.Getrlimit(syscall.RLIMIT_NOFILE, &files) == nil {
		files.Cur = min(files.Max, 1024)
		syscall.Setrlimit(syscall.RLIMIT_NOFILE, &files)
	}
	cmd := exec.CommandContext(ctx, args[0], args[1:]...)
	cmd.Stdout, cmd.Stderr = os.Stdout, os.Stderr
	return RunTree(cmd)
}
