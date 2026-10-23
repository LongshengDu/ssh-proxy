//go:build windows || darwin

package ui

import (
	"context"
	"errors"
	"fmt"
	"log"
	"sync"

	"ssh_proxy/src/config"
	"ssh_proxy/src/control"
)

// Backend is what the tray needs from the proxy. The CLI owns validation,
// login setup and the lifecycle; status is read from the supervisor's control API.
type Backend interface {
	Status() control.Status
	Check(ctx context.Context) (config.ConfigCheck, error)
	CheckLogin(ctx context.Context) error // Silent, strict login check; never prompts.
	Trust(ctx context.Context) error      // Interactive setup in a real terminal.
	Start(ctx context.Context) error
	Stop(ctx context.Context) error
	AutoStart() (bool, error) // start_proxy_on_launch in app.json.
	SetAutoStart(enabled bool) error
}

// Desktop holds the tray's OS integrations.
type Desktop interface {
	StartupEnabled() (bool, error)
	SetStartup(enabled bool) error
	Open(path string) error
	OpenEditable(path string) error
	RunScript(ctx context.Context, script Script) error
	ShowError(err error)
	ShowProxyError(err error, logPath string)
}

// View shows the menu model and tooltip.
type View interface {
	Render(items []Item, tooltip string)
}

// Controller keeps the tray's view of the backend and serializes user actions.
// Its methods run on the tray's event loop; long actions return a job that the
// loop runs elsewhere and hands back to Finish.
type Controller struct {
	backend  Backend
	desktop  Desktop
	view     View
	logger   *log.Logger
	paths    config.Paths
	location string
	scripts  []Script

	st           control.Status
	startup      bool
	startupErr   error
	autoStart    bool
	autoStartErr error
	action       int // Nonzero while a job is running.
	lastError    string

	mu      sync.Mutex // Guards prompts, which jobs also use.
	prompts SetupPrompts
}

func NewController(backend Backend, desktop Desktop, view View, logger *log.Logger, paths config.Paths, location string, scripts []Script) *Controller {
	return &Controller{backend: backend, desktop: desktop, view: view, logger: logger, paths: paths, location: location,
		scripts: scripts, st: control.Status{Phase: phaseChecking}, prompts: SetupPrompts{}}
}

func (c *Controller) report(err error) {
	c.logger.Print(err)
	c.desktop.ShowError(err)
}

func (c *Controller) render() {
	state := MenuState{Status: c.st, Startup: c.startup, StartupKnown: c.startupErr == nil,
		AutoStart: c.autoStart, AutoStartKnown: c.autoStartErr == nil, Busy: c.action != 0, Exiting: c.action == StopAndQuit}
	c.view.Render(Menu(state, c.scripts...), "SSH Proxy — "+string(c.st.Phase))
}

// CheckConfig validates through the CLI, including native ssh -G, and opens a
// failing SSH configuration once per tray run. It reports whether it is valid.
func (c *Controller) CheckConfig(ctx context.Context) bool {
	err := c.checkConfig(ctx)
	if err != nil && ctx.Err() == nil {
		c.report(err)
	}
	return err == nil
}

// Launch runs once when the tray starts. It checks the configuration and, when
// start_proxy_on_launch is set in app.json and no proxy runs yet, returns the same
// start job as the Start item. Invalid configuration never starts setup.
func (c *Controller) Launch(ctx context.Context) func() error {
	valid := c.CheckConfig(ctx)
	c.Refresh(ctx)
	if !valid || !c.autoStart || c.st.PID != 0 {
		return nil
	}
	c.logger.Print("Starting the proxy at app launch (start_proxy_on_launch in app.json)")
	return c.Handle(ctx, ToggleProxy)
}

func (c *Controller) checkConfig(ctx context.Context) error {
	result, err := c.backend.Check(ctx)
	if err == nil && !result.Valid {
		err = errors.New(result.Error)
		c.mu.Lock()
		path := c.prompts.Config(result)
		c.mu.Unlock()
		if path != "" && path != c.paths.Config {
			c.logger.Printf("Opening configuration for setup: %s", path)
			err = errors.Join(err, c.desktop.OpenEditable(path))
		}
	}
	return err
}

// Refresh reads status and login registration, reports each new problem once
// and updates the menu.
func (c *Controller) Refresh(ctx context.Context) {
	previous := c.st
	c.st = c.backend.Status()
	c.startup, c.startupErr = c.desktop.StartupEnabled()
	// An unreadable app.json disables the item.
	c.autoStart, c.autoStartErr = c.backend.AutoStart()
	issue := c.startupErr
	if c.st.UncleanExit {
		if previous.PID != 0 && previous.Phase != control.PhaseStopped && c.action != StopAndQuit && c.action != ToggleProxy && ctx.Err() == nil {
			c.logger.Print(c.st.LastError)
			c.desktop.ShowProxyError(errors.New(c.st.LastError), c.st.Log)
		}
	} else if c.st.LastError != "" {
		issue = errors.Join(issue, errors.New(c.st.LastError))
	}
	message := ""
	if issue != nil {
		message = issue.Error()
	}
	if message != "" && message != c.lastError && ctx.Err() == nil {
		c.report(issue)
	}
	c.lastError = message
	if c.st.Phase != previous.Phase || c.st.PID != previous.PID || c.st.Endpoint != previous.Endpoint {
		c.logger.Printf("Proxy status: %s; pid=%d; endpoint=%s; SOCKS5=%s; HTTP=%s", c.st.Phase, c.st.PID, c.st.Destination, c.st.SOCKS, c.st.HTTP)
	}
	c.render()
}

// Handle runs file actions at once. Proxy, login and script actions mark the
// tray busy and return a job; the caller runs it and passes its result to Finish.
func (c *Controller) Handle(ctx context.Context, id int) func() error {
	if id >= OpenLog && id <= OpenLocation {
		if err := c.open(id); err != nil {
			c.report(err)
		}
		return nil
	}
	if c.action != 0 || (id >= ScriptBase && c.st.Phase != control.PhaseRunning) {
		return nil
	}
	var job func() error
	active, startup, autoStart := c.st.PID != 0, c.startup, c.autoStart
	switch {
	case id >= ScriptBase && id-ScriptBase < len(c.scripts):
		script := c.scripts[id-ScriptBase]
		c.logger.Printf("Run: %s (%s)", script.Name, script.Path)
		job = func() error { return c.desktop.RunScript(ctx, script) }
	case id == ToggleStartup:
		job = func() error { return c.desktop.SetStartup(!startup) }
	case id == ToggleAutoStart:
		job = func() error { return c.backend.SetAutoStart(!autoStart) }
	case id == StopAndQuit:
		c.logger.Print("Stopping proxy before exiting app")
		job = func() error { return c.backend.Stop(ctx) }
	case id == ToggleProxy && active:
		job = func() error { return c.backend.Stop(ctx) }
	case id == ToggleProxy:
		job = func() error { return c.startProxy(ctx) }
	default:
		return nil
	}
	c.action = id
	c.render()
	return job
}

// Finish reports a job's failure and requests exit only after a successful stop.
func (c *Controller) Finish(ctx context.Context, id int, err error) bool {
	c.action = 0
	if err != nil && ctx.Err() == nil {
		c.report(err)
	}
	return id == StopAndQuit && err == nil && ctx.Err() == nil
}

// startProxy is shared by the Start item and launch auto-start. It validates
// the configuration (opening a failing file once), checks unattended login
// silently, and opens interactive SSH setup only when that check fails. A
// successful setup is checked again: a password login alone cannot run the
// background proxy. Any failure leaves the proxy stopped; nothing retries.
func (c *Controller) startProxy(ctx context.Context) error {
	if err := c.checkConfig(ctx); err != nil {
		return err
	}
	if err := c.backend.CheckLogin(ctx); err != nil {
		if err := ctx.Err(); err != nil {
			return err
		}
		// CLI autostart may have won while SSH login was being checked.
		if st := c.backend.Status(); st.PID != 0 && st.Phase != control.PhaseStopped {
			return nil
		}
		c.logger.Printf("Unattended SSH login check failed; opening SSH setup terminal: %v", err)
		if err := c.backend.Trust(ctx); err != nil {
			return err
		}
		if err := ctx.Err(); err != nil {
			return err
		}
		if err := c.backend.CheckLogin(ctx); err != nil {
			return fmt.Errorf("SSH setup finished, but unattended login still fails; set up key or ssh-agent authentication (see ssh_config): %w", err)
		}
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	return c.backend.Start(ctx)
}

func (c *Controller) open(id int) error {
	switch id {
	case OpenLog:
		return c.desktop.Open(c.paths.Log)
	case OpenConfig:
		return c.desktop.Open(c.paths.Config)
	case OpenSSHConfig:
		path, err := config.SSHConfigPath(c.paths.Config)
		if err != nil {
			return err
		}
		return c.desktop.OpenEditable(path)
	case OpenLocation:
		return c.desktop.Open(c.location)
	}
	return nil
}
