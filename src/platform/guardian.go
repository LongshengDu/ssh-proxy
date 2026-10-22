package platform

import (
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"os/signal"
	"syscall"
	"time"
)

// RunGuardian is the _ssh helper used on macOS and Linux. EOF means the owning supervisor exited.
func RunGuardian(args []string) error {
	if len(args) == 0 {
		return fmt.Errorf("missing SSH executable")
	}
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()
	go func() { io.Copy(io.Discard, os.Stdin); cancel() }()
	cmd := exec.CommandContext(ctx, args[0], args[1:]...)
	cmd.WaitDelay = 3 * time.Second
	cmd.Stdout, cmd.Stderr = os.Stdout, os.Stderr
	return cmd.Run()
}
