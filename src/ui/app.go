//go:build windows || darwin

package ui

import (
	"errors"
	"flag"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"

	"ssh_proxy/src/proxy"
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
	f := flag.NewFlagSet("ssh_proxy_app", flag.ContinueOnError)
	config := f.String("config", "", "Path to config.toml")
	backend := f.String("backend", "", "Path to ssh_proxy_cli executable")
	smoke := f.Bool("smoke-test", false, "Create the tray icon and menu, then exit")
	parseErr := f.Parse(args)
	if errors.Is(parseErr, flag.ErrHelp) {
		return nil
	}
	if *backend == "" {
		exe, err := os.Executable()
		if err != nil {
			return err
		}
		*backend = defaultBackend(exe)
	}
	abs, err := filepath.Abs(*backend)
	if err != nil {
		return err
	}
	p, err := proxy.PathsForExecutable(*config, abs)
	if err != nil {
		return err
	}
	logger, writer, err := proxy.NewRunLog(p, "tray", false)
	if err != nil {
		return err
	}
	logPath, p.Log = writer.Name(), writer.Name()
	defer writer.Close()
	defer func() {
		if runErr != nil {
			logger.Printf("Error: %v", runErr)
		}
		logger.Print("Tray exited; proxy unchanged")
	}()
	slog.SetDefault(slog.New(slog.NewTextHandler(writer, nil)))
	logger.Printf("Tray started; pid=%d; backend=%s; config=%s", os.Getpid(), abs, p.Config)
	if parseErr != nil {
		return parseErr
	}
	if _, err := os.Stat(abs); err != nil {
		return fmt.Errorf("backend missing: %w", err)
	}
	return run(p, abs, *smoke, logger, writer)
}
