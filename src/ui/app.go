//go:build windows || darwin

package ui

import (
	"errors"
	"fmt"
	"log/slog"
	"os"

	"ssh-proxy/src/config"
	"ssh-proxy/src/logs"
	"ssh-proxy/src/platform"
)

// Run creates the app's one continuous log before checking its configuration
// or backend, so initialization and later failures all use the same file.
func Run(args []string) (runErr error) {
	var logPath string
	defer func() {
		if runErr != nil {
			showError(runErr, logPath)
		}
	}()
	exe, err := os.Executable()
	if err != nil {
		return err
	}
	backend := defaultBackend(exe)
	p, err := config.PathsForExecutable(backend)
	if err != nil {
		return err
	}
	unlock, err := platform.LockInstance(p.ID + "-tray")
	if errors.Is(err, platform.ErrAlreadyRunning) {
		return fmt.Errorf("SSH Proxy app is already running")
	}
	if err != nil {
		return err
	}
	defer unlock()
	logger, writer, err := logs.NewRunLog(p, "tray", false)
	if err != nil {
		return err
	}
	logPath, p.Log = writer.Name(), writer.Name()
	defer writer.Close()
	defer func() {
		if runErr != nil {
			logger.Printf("Error: %v", runErr)
		}
		logger.Print("Tray exited; app shutdown complete")
	}()
	slog.SetDefault(slog.New(slog.NewTextHandler(writer, nil)))
	logger.Printf("Tray started; pid=%d; backend=%s; config=%s", os.Getpid(), backend, p.Config)
	if len(args) != 0 {
		return fmt.Errorf("ssh-proxy-app takes no command-line arguments; use ssh-proxy-cli for terminal commands")
	}
	if _, err := os.Stat(backend); err != nil {
		return fmt.Errorf("backend missing: %w", err)
	}
	// A registry or LaunchAgents problem must not keep the app from opening.
	if err := RepairStartup(p.ID, exe); err != nil {
		logger.Printf("Cannot repair login startup: %v", err)
	}
	return run(p, backend, logger, writer)
}
