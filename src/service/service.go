// Package service registers the supervisor as an OS service for the current user.
package service

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/user"
	"path/filepath"
	"runtime"
	"time"

	kardianos "github.com/kardianos/service"
	"golang.org/x/term"

	"ssh_proxy/src/config"
	"ssh_proxy/src/control"
	"ssh_proxy/src/supervisor"
)

func serviceReport(name string, installed bool, state string, startup bool) error {
	if !installed {
		_, err := fmt.Printf("Service %s is not installed.\n", name)
		return err
	}
	setting := "disabled"
	if startup {
		setting = "enabled"
	}
	_, err := fmt.Printf("Service: %s\nState: %s\nAutomatic startup: %s\n", name, state, setting)
	return err
}

type serviceProgram struct {
	paths  config.Paths
	cancel context.CancelFunc
	done   chan error
}

func (p *serviceProgram) Start(kardianos.Service) error {
	ctx, cancel := context.WithCancel(context.Background())
	p.cancel, p.done = cancel, make(chan error, 1)
	go func() {
		err := supervisor.RunContext(ctx, p.paths, false, true)
		p.done <- err
		if ctx.Err() == nil {
			// An unexpected supervisor exit must be visible to OS recovery, not leave
			// a "running" service that no longer forwards any traffic.
			// The supervisor already recorded the outcome in this run's log.
			fmt.Fprintln(os.Stderr, "service supervisor exited:", err)
			os.Exit(1)
		}
	}()
	return nil
}
func (p *serviceProgram) Stop(kardianos.Service) error {
	if p.cancel == nil {
		return nil
	}
	p.cancel()
	select {
	case err := <-p.done:
		return err
	case <-time.After(20 * time.Second):
		return fmt.Errorf("supervisor shutdown timed out")
	}
}

func serviceName(id string) string { return "com.sshproxy." + id }
func serviceFor(p config.Paths) (kardianos.Service, *kardianos.Config, error) {
	exe, err := os.Executable()
	if err != nil {
		return nil, nil, err
	}
	cfg := &kardianos.Config{Name: serviceName(p.ID), DisplayName: "SSH Proxy (" + p.ID + ")",
		Description: "Native OpenSSH SOCKS5 tunnel and HTTP proxy", Executable: exe,
		Arguments: []string{"_service", "--config", p.Config}, WorkingDirectory: filepath.Dir(p.Config),
		EnvVars: map[string]string{"SSH_PROXY_RUNTIME": p.Runtime},
		Option: kardianos.KeyValue{"UserService": runtime.GOOS == "darwin", "RunAtLoad": true,
			"KeepAlive": true, "LogOutput": false,
			"StartType": "automatic", "DelayedAutoStart": true,
			"OnFailure": "restart", "OnFailureDelayDuration": "5s", "OnFailureResetPeriod": 300}}
	if err = configureService(cfg); err != nil {
		return nil, nil, err
	}
	svc, err := kardianos.New(&serviceProgram{paths: p}, cfg)
	return svc, cfg, err
}

// Run is the _service entry point that the OS service manager starts.
func Run(p config.Paths) error {
	svc, _, err := serviceFor(p)
	if err != nil {
		return err
	}
	return svc.Run()
}

// installedServiceAction returns handled=false only when no service was installed.
// Permission failures never silently create an unmanaged duplicate instance.
func installedServiceAction(p config.Paths, action string) (bool, error) {
	if present, err := servicePresent(p.ID); err != nil || !present {
		return present, err
	}
	svc, _, err := serviceFor(p)
	if err != nil {
		return false, err
	}
	state, err := svc.Status()
	if errors.Is(err, kardianos.ErrNotInstalled) {
		return false, nil
	}
	if err != nil {
		return true, err
	}
	return true, controlService(svc, state, action)
}

func controlService(svc kardianos.Service, state kardianos.Status, action string) error {
	if (action == "start" && state == kardianos.StatusRunning) || (action == "stop" && state == kardianos.StatusStopped) {
		return nil
	}
	if action == "restart" && state == kardianos.StatusStopped {
		action = "start"
	}
	return kardianos.Control(svc, action)
}

// Manage runs one service subcommand: install, uninstall, status, enable or
// disable. The CLI parser rejects other names and extra arguments.
func Manage(p config.Paths, action string) error {
	if present, err := servicePresent(p.ID); err != nil {
		return err
	} else if !present && action != "install" {
		if action == "status" || action == "uninstall" {
			return serviceReport(serviceName(p.ID), false, "not_installed", false)
		}
		return fmt.Errorf("service is not installed")
	}
	svc, cfg, err := serviceFor(p)
	if err != nil {
		return err
	}
	if action == "install" {
		if _, err = config.LoadConfig(p.Config); err != nil {
			return err
		}
		if _, err := control.Call(p, "GET", "/status"); err == nil {
			return fmt.Errorf("stop the current background instance before installing its service")
		}
		if runtime.GOOS == "windows" {
			current, err := user.Current()
			if err != nil {
				return err
			}
			cfg.UserName = current.Username
			if !term.IsTerminal(int(os.Stdin.Fd())) {
				return fmt.Errorf("service install requires an interactive terminal for the Windows account password (%s)", cfg.UserName)
			}
			fmt.Fprintf(os.Stderr, "Password for %s (not saved in project files): ", cfg.UserName)
			password, err := term.ReadPassword(int(os.Stdin.Fd()))
			fmt.Fprintln(os.Stderr)
			if err != nil {
				return err
			}
			cfg.Option["Password"] = string(password)
			defer func() {
				for i := range password {
					password[i] = 0
				}
				delete(cfg.Option, "Password")
			}()
		}
		if err = os.MkdirAll(p.Logs, 0700); err != nil {
			return err
		}
		if err = svc.Install(); err != nil {
			return err
		}
		if err = grantServiceControl(cfg.Name); err != nil {
			rollback := svc.Uninstall()
			return fmt.Errorf("service control permissions: %v (installation rollback: %v)", err, rollback)
		}
		return serviceReport(cfg.Name, true, "stopped", true)
	}
	state, err := svc.Status()
	if errors.Is(err, kardianos.ErrNotInstalled) && (action == "status" || action == "uninstall") {
		return serviceReport(cfg.Name, false, "not_installed", false)
	}
	if err != nil {
		return err
	}
	label := "stopped"
	if state == kardianos.StatusRunning {
		label = "running"
	}
	switch action {
	case "status":
		enabled, e := ServiceAutoStart(cfg.Name, "status")
		if e != nil {
			return e
		}
		return serviceReport(cfg.Name, true, label, enabled)
	case "uninstall":
		if state == kardianos.StatusRunning {
			if err = svc.Stop(); err != nil {
				return err
			}
		}
		if err = svc.Uninstall(); err != nil {
			return err
		}
		return serviceReport(cfg.Name, false, "not_installed", false)
	case "enable", "disable":
		enabled, e := ServiceAutoStart(cfg.Name, action)
		if e != nil {
			return e
		}
		return serviceReport(cfg.Name, true, label, enabled)
	default:
		return fmt.Errorf("unknown service command %q", action)
	}
}

// Action sends start, stop or restart to an installed service. It returns
// handled=false when no service is installed or a non-service instance runs.
func Action(p config.Paths, action string, wait time.Duration) (bool, error) {
	// An authenticated foreground/background instance takes priority over a
	// stopped service, so stop/restart always reaches the active instance.
	if st, err := control.Call(p, "GET", "/status"); err == nil && !st.Service {
		return false, nil
	}
	handled, err := installedServiceAction(p, action)
	if handled && err == nil && wait > 0 {
		err = waitServiceReady(p, wait)
	}
	return handled, err
}

func waitServiceReady(p config.Paths, wait time.Duration) error {
	deadline := time.Now().Add(wait)
	for time.Now().Before(deadline) {
		if st, err := control.Call(p, "GET", "/status"); err == nil {
			if st.Phase == control.PhaseRunning || st.Phase == control.PhaseDegraded {
				return nil
			}
		}
		time.Sleep(200 * time.Millisecond)
	}
	if _, err := control.Call(p, "GET", "/status"); err == nil {
		return nil
	}
	return fmt.Errorf("service has not exposed its control API; inspect %s", config.LatestLog(p.Logs, p.ID))
}
