package proxy

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/user"
	"path/filepath"
	"runtime"
	"time"

	"github.com/kardianos/service"
	"golang.org/x/term"
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
	paths  Paths
	cancel context.CancelFunc
	done   chan error
}

func (p *serviceProgram) Start(service.Service) error {
	ctx, cancel := context.WithCancel(context.Background())
	p.cancel, p.done = cancel, make(chan error, 1)
	go func() {
		err := runSupervisorContext(context.WithValue(ctx, serviceModeKey{}, true), p.paths, false)
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
func (p *serviceProgram) Stop(service.Service) error {
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
func serviceFor(p Paths) (service.Service, *service.Config, error) {
	exe, err := os.Executable()
	if err != nil {
		return nil, nil, err
	}
	cfg := &service.Config{Name: serviceName(p.ID), DisplayName: "SSH Proxy (" + p.ID + ")",
		Description: "Native OpenSSH SOCKS5 tunnel and HTTP proxy", Executable: exe,
		Arguments: []string{"_service", "--config", p.Config}, WorkingDirectory: filepath.Dir(p.Config),
		EnvVars: map[string]string{"SSH_PROXY_RUNTIME": p.Runtime},
		Option: service.KeyValue{"UserService": runtime.GOOS == "darwin", "RunAtLoad": true,
			"KeepAlive": true, "LogOutput": false,
			"StartType": "automatic", "DelayedAutoStart": true,
			"OnFailure": "restart", "OnFailureDelayDuration": "5s", "OnFailureResetPeriod": 300}}
	if err = configureService(cfg); err != nil {
		return nil, nil, err
	}
	svc, err := service.New(&serviceProgram{paths: p}, cfg)
	return svc, cfg, err
}

func runService(p Paths) error {
	svc, _, err := serviceFor(p)
	if err != nil {
		return err
	}
	return svc.Run()
}

// installedServiceAction returns handled=false only when no service was installed.
// Permission failures never silently create an unmanaged duplicate instance.
func installedServiceAction(p Paths, action string) (bool, error) {
	if present, err := servicePresent(p.ID); err != nil || !present {
		return present, err
	}
	svc, _, err := serviceFor(p)
	if err != nil {
		return false, err
	}
	state, err := svc.Status()
	if errors.Is(err, service.ErrNotInstalled) {
		return false, nil
	}
	if err != nil {
		return true, err
	}
	return true, controlService(svc, state, action)
}

func controlService(svc service.Service, state service.Status, action string) error {
	if (action == "start" && state == service.StatusRunning) || (action == "stop" && state == service.StatusStopped) {
		return nil
	}
	if action == "restart" && state == service.StatusStopped {
		action = "start"
	}
	return service.Control(svc, action)
}

func manageService(p Paths, args []string) error {
	if len(args) == 0 || args[0] == "--help" || args[0] == "-h" {
		return writeHelp(os.Stdout, "service", nil)
	}
	switch args[0] {
	case "install", "uninstall", "status", "enable", "disable":
	case "start", "stop", "restart":
		return fmt.Errorf("use %s directly; service %s is not a command", args[0], args[0])
	default:
		return fmt.Errorf("unknown service command %q", args[0])
	}
	if args[0] == "install" {
		f, _ := commandFlags("service install")
		if err := f.Parse(args[1:]); err != nil {
			return err
		}
		if f.NArg() != 0 {
			return fmt.Errorf("unexpected install arguments")
		}
	} else if len(args) != 1 {
		return fmt.Errorf("unexpected service arguments")
	}
	if present, err := servicePresent(p.ID); err != nil {
		return err
	} else if !present && args[0] != "install" {
		if len(args) == 1 && (args[0] == "status" || args[0] == "uninstall") {
			return serviceReport(serviceName(p.ID), false, "not_installed", false)
		}
		return fmt.Errorf("service is not installed")
	}
	svc, cfg, err := serviceFor(p)
	if err != nil {
		return err
	}
	if args[0] == "install" {
		if _, err = LoadConfig(p.Config); err != nil {
			return err
		}
		if _, err := controlCall(p, "GET", "/status"); err == nil {
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
	if errors.Is(err, service.ErrNotInstalled) && (args[0] == "status" || args[0] == "uninstall") {
		return serviceReport(cfg.Name, false, "not_installed", false)
	}
	if err != nil {
		return err
	}
	label := "stopped"
	if state == service.StatusRunning {
		label = "running"
	}
	switch args[0] {
	case "status":
		enabled, e := ServiceAutoStart(cfg.Name, "status")
		if e != nil {
			return e
		}
		return serviceReport(cfg.Name, true, label, enabled)
	case "uninstall":
		if state == service.StatusRunning {
			if err = svc.Stop(); err != nil {
				return err
			}
		}
		if err = svc.Uninstall(); err != nil {
			return err
		}
		return serviceReport(cfg.Name, false, "not_installed", false)
	case "enable", "disable":
		enabled, e := ServiceAutoStart(cfg.Name, args[0])
		if e != nil {
			return e
		}
		return serviceReport(cfg.Name, true, label, enabled)
	default:
		return fmt.Errorf("unknown service command %q", args[0])
	}
}

func serviceCommand(p Paths, args []string) (bool, error) {
	switch args[0] {
	case "service":
		return true, manageService(p, args[1:])
	case "_service":
		if len(args) != 1 {
			return true, fmt.Errorf("unexpected service runner arguments")
		}
		return true, runService(p)
	}
	return false, nil
}

func serviceAction(p Paths, action string, wait time.Duration) (bool, error) {
	// An authenticated foreground/background instance takes priority over a
	// stopped service, so stop/restart always reaches the active instance.
	if st, err := controlCall(p, "GET", "/status"); err == nil && !st.Service {
		return false, nil
	}
	handled, err := installedServiceAction(p, action)
	if handled && err == nil && wait > 0 {
		err = waitServiceReady(p, wait)
	}
	return handled, err
}

func waitServiceReady(p Paths, wait time.Duration) error {
	deadline := time.Now().Add(wait)
	for time.Now().Before(deadline) {
		if st, err := controlCall(p, "GET", "/status"); err == nil {
			if st.Phase == PhaseRunning || st.Phase == PhaseDegraded {
				return nil
			}
		}
		time.Sleep(200 * time.Millisecond)
	}
	if _, err := controlCall(p, "GET", "/status"); err == nil {
		return nil
	}
	return fmt.Errorf("service has not exposed its control API; inspect %s", latestLog(p.Logs, p.ID))
}
