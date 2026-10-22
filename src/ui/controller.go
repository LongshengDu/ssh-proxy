//go:build windows || darwin

package ui

import (
	"context"
	"errors"
	"log"
	"sync"

	"ssh_proxy/src/config"
	"ssh_proxy/src/control"
)

// Backend is what the tray needs from the proxy. The CLI owns validation,
// trust and the lifecycle; status is read from the supervisor's control API.
type Backend interface {
	Status() control.Status
	Check(ctx context.Context) (config.ConfigCheck, error)
	Trust(ctx context.Context) error // Interactive setup in a real terminal.
	Start(ctx context.Context) error
	Stop(ctx context.Context) error
}

// Desktop holds the tray's OS integrations.
type Desktop interface {
	StartupEnabled() (bool, error)
	SetStartup(enabled bool) error
	Open(path string) error
	OpenEditable(path string) error
	RunScript(ctx context.Context, script Script) error
	ShowError(err error)
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

	st         control.Status
	startup    bool
	startupErr error
	busy       bool
	lastError  string

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
	c.view.Render(Menu(c.st, c.startup, c.startupErr == nil, c.busy, c.scripts...), "SSH Proxy — "+string(c.st.Phase))
}

// CheckConfig validates through the CLI, including native ssh -G, and opens a
// failing SSH configuration once per tray run. It reports whether it is valid.
func (c *Controller) CheckConfig(ctx context.Context) bool {
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
	if err != nil && ctx.Err() == nil {
		c.report(err)
	}
	return err == nil
}

// Refresh reads status and login registration, reports each new problem once
// and updates the menu.
func (c *Controller) Refresh(ctx context.Context) {
	previous := c.st
	c.st = c.backend.Status()
	c.startup, c.startupErr = c.desktop.StartupEnabled()
	issue := c.startupErr
	if c.st.LastError != "" {
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
	if c.busy || (id >= ScriptBase && c.st.Phase != control.PhaseRunning) {
		return nil
	}
	var job func() error
	active, startup := c.st.PID != 0, c.startup
	switch {
	case id >= ScriptBase && id-ScriptBase < len(c.scripts):
		script := c.scripts[id-ScriptBase]
		c.logger.Printf("Run: %s (%s)", script.Name, script.Path)
		job = func() error { return c.desktop.RunScript(ctx, script) }
	case id == ToggleStartup:
		job = func() error { return c.desktop.SetStartup(!startup) }
	case id == ToggleProxy && active:
		job = func() error { return c.backend.Stop(ctx) }
	case id == ToggleProxy:
		job = func() error { return c.startProxy(ctx) }
	default:
		return nil
	}
	c.busy = true
	c.render()
	return job
}

// Finish ends a job from Handle and reports its failure.
func (c *Controller) Finish(ctx context.Context, id int, err error) {
	c.busy = false
	if err != nil && ctx.Err() == nil {
		c.report(err)
		if id == ToggleProxy {
			c.CheckConfig(ctx)
		}
	}
}

func (c *Controller) startProxy(ctx context.Context) error {
	c.logger.Print("Running SSH trust before proxy start")
	if err := c.backend.Trust(ctx); err != nil {
		return err
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
