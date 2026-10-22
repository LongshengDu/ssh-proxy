//go:build windows || darwin

package tests

import (
	"context"
	"errors"
	"io"
	"log"
	"slices"
	"strings"
	"testing"

	"ssh_proxy/src/config"
	"ssh_proxy/src/control"
	"ssh_proxy/src/ui"
)

type fakeBackend struct {
	status                      control.Status
	check                       config.ConfigCheck
	logins                      []error // Results of successive silent login checks; nil once exhausted.
	onLogin                     func()
	trustErr, startErr, stopErr error
	autoStart                   bool
	calls                       []string
}

func (b *fakeBackend) Status() control.Status { return b.status }
func (b *fakeBackend) Check(context.Context) (config.ConfigCheck, error) {
	b.calls = append(b.calls, "check")
	return b.check, nil
}
func (b *fakeBackend) CheckLogin(context.Context) error {
	b.calls = append(b.calls, "login")
	if b.onLogin != nil {
		b.onLogin()
	}
	if len(b.logins) == 0 {
		return nil
	}
	err := b.logins[0]
	b.logins = b.logins[1:]
	return err
}
func (b *fakeBackend) Trust(context.Context) error {
	b.calls = append(b.calls, "trust")
	return b.trustErr
}
func (b *fakeBackend) Start(context.Context) error {
	b.calls = append(b.calls, "start")
	if b.startErr == nil {
		b.status = control.Status{Phase: control.PhaseRunning, PID: 7}
	}
	return b.startErr
}
func (b *fakeBackend) Stop(context.Context) error {
	b.calls = append(b.calls, "stop")
	if b.stopErr == nil {
		b.status = control.Status{Phase: control.PhaseStopped}
	}
	return b.stopErr
}
func (b *fakeBackend) AutoStart() (bool, error) { return b.autoStart, nil }
func (b *fakeBackend) SetAutoStart(enabled bool) error {
	b.calls = append(b.calls, "autostart")
	b.autoStart = enabled
	return nil
}

type fakeDesktop struct {
	startup                  bool
	opened, edited, messages []string
	scripts                  []string
}

func (d *fakeDesktop) StartupEnabled() (bool, error)  { return d.startup, nil }
func (d *fakeDesktop) SetStartup(enabled bool) error  { d.startup = enabled; return nil }
func (d *fakeDesktop) Open(path string) error         { d.opened = append(d.opened, path); return nil }
func (d *fakeDesktop) OpenEditable(path string) error { d.edited = append(d.edited, path); return nil }
func (d *fakeDesktop) RunScript(_ context.Context, script ui.Script) error {
	d.scripts = append(d.scripts, script.Name)
	return nil
}
func (d *fakeDesktop) ShowError(err error) { d.messages = append(d.messages, err.Error()) }

type fakeView struct {
	items   []ui.Item
	tooltip string
}

func (v *fakeView) Render(items []ui.Item, tooltip string) { v.items, v.tooltip = items, tooltip }
func (v *fakeView) item(id int) ui.Item {
	index := slices.IndexFunc(v.items, func(item ui.Item) bool { return item.ID == id })
	if index < 0 {
		return ui.Item{}
	}
	return v.items[index]
}

var (
	validConfig    = config.ConfigCheck{Valid: true}
	missingHosts   = config.ConfigCheck{Valid: false, EditFile: "ssh_config", Error: "add your hosts"}
	errLoginFailed = errors.New("Permission denied (publickey)")
)

func newController(backend *fakeBackend, scripts ...ui.Script) (*ui.Controller, *fakeDesktop, *fakeView) {
	desktop, view := &fakeDesktop{}, &fakeView{}
	paths := config.Paths{Config: "config.toml", Log: "tray.txt", ID: "0123456789abcdef"}
	return ui.NewController(backend, desktop, view, log.New(io.Discard, "", 0), paths, "folder", scripts), desktop, view
}

// run performs an action the way the tray loop does, with the job inline.
func run(ctx context.Context, c *ui.Controller, id int) bool {
	job := c.Handle(ctx, id)
	if job == nil {
		return false
	}
	c.Finish(ctx, id, job())
	c.Refresh(ctx)
	return true
}

func startProxy(t *testing.T, backend *fakeBackend) (*fakeDesktop, []string) {
	t.Helper()
	ctx := context.Background()
	c, desktop, _ := newController(backend)
	c.Refresh(ctx)
	if !run(ctx, c, ui.ToggleProxy) {
		t.Fatal("Start was not accepted")
	}
	return desktop, backend.calls
}

func TestTrayControllerReportsEachProblemOnce(t *testing.T) {
	ctx := context.Background()
	backend := &fakeBackend{status: control.Status{Phase: control.PhaseDegraded, PID: 7, LastError: "probe failed"}}
	c, desktop, view := newController(backend)
	for range 3 {
		c.Refresh(ctx)
	}
	backend.status.LastError = "SSH exited"
	c.Refresh(ctx)
	backend.status = control.Status{Phase: control.PhaseRunning, PID: 7}
	c.Refresh(ctx)
	backend.status.LastError = "probe failed"
	c.Refresh(ctx)
	if want := []string{"probe failed", "SSH exited", "probe failed"}; !slices.Equal(desktop.messages, want) {
		t.Fatalf("got %q, want %q", desktop.messages, want)
	}
	if view.tooltip != "SSH Proxy — running" || view.item(ui.ToggleProxy).Label != "Stop proxy" {
		t.Fatalf("menu not rendered from status: %q %+v", view.tooltip, view.items)
	}
}

func TestTrayControllerSilentLoginStartsWithoutTerminal(t *testing.T) {
	_, calls := startProxy(t, &fakeBackend{status: control.Status{Phase: control.PhaseStopped}, check: validConfig})
	if want := []string{"check", "login", "start"}; !slices.Equal(calls, want) {
		t.Fatalf("got %q, want %q", calls, want)
	}
}

func TestTrayControllerInteractiveSetupThenRecheck(t *testing.T) {
	_, calls := startProxy(t, &fakeBackend{status: control.Status{Phase: control.PhaseStopped}, check: validConfig, logins: []error{errLoginFailed, nil}})
	if want := []string{"check", "login", "trust", "login", "start"}; !slices.Equal(calls, want) {
		t.Fatalf("got %q, want %q", calls, want)
	}
}

func TestTrayControllerPasswordOnlyLoginDoesNotStart(t *testing.T) {
	desktop, calls := startProxy(t, &fakeBackend{status: control.Status{Phase: control.PhaseStopped}, check: validConfig, logins: []error{errLoginFailed, errLoginFailed}})
	if want := []string{"check", "login", "trust", "login"}; !slices.Equal(calls, want) {
		t.Fatalf("got %q, want %q", calls, want)
	}
	if len(desktop.messages) != 1 || !strings.Contains(desktop.messages[0], "unattended login still fails") || !strings.Contains(desktop.messages[0], "publickey") {
		t.Fatal("password-only setup must explain that key or agent authentication is needed", desktop.messages)
	}
}

func TestTrayControllerCancelledSetupDoesNotStart(t *testing.T) {
	desktop, calls := startProxy(t, &fakeBackend{status: control.Status{Phase: control.PhaseStopped}, check: validConfig,
		logins: []error{errLoginFailed}, trustErr: errors.New("SSH setup exited with code 7")})
	if want := []string{"check", "login", "trust"}; !slices.Equal(calls, want) {
		t.Fatalf("got %q, want %q", calls, want)
	}
	if !slices.Equal(desktop.messages, []string{"SSH setup exited with code 7"}) {
		t.Fatal("setup failure must be reported once", desktop.messages)
	}
}

func TestTrayControllerClosingDuringLoginDoesNotOpenTerminal(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	backend := &fakeBackend{check: validConfig, logins: []error{context.Canceled}, onLogin: cancel}
	c, desktop, _ := newController(backend)
	run(ctx, c, ui.ToggleProxy)
	if !slices.Equal(backend.calls, []string{"check", "login"}) || len(desktop.messages) != 0 {
		t.Fatal("closing the app must not open setup, start a proxy or show errors", backend.calls, desktop.messages)
	}
}

func TestTrayControllerInvalidConfigBlocksStartAndOpensOnce(t *testing.T) {
	ctx := context.Background()
	backend := &fakeBackend{status: control.Status{Phase: control.PhaseStopped}, check: missingHosts}
	c, desktop, view := newController(backend)
	c.Refresh(ctx)
	for range 2 {
		run(ctx, c, ui.ToggleProxy)
	}
	if !slices.Equal(backend.calls, []string{"check", "check"}) {
		t.Fatal("invalid configuration must stop before SSH setup", backend.calls)
	}
	if !slices.Equal(desktop.edited, []string{"ssh_config"}) || len(desktop.messages) != 2 {
		t.Fatal("each Start reports the reason but opens the file once", desktop.edited, desktop.messages)
	}
	if view.item(ui.ToggleProxy).Disabled {
		t.Fatal("Start must stay available to retry after corrections")
	}
	backend.check, backend.calls = validConfig, nil
	run(ctx, c, ui.ToggleProxy)
	if !slices.Equal(backend.calls, []string{"check", "login", "start"}) {
		t.Fatal("corrected configuration did not start", backend.calls)
	}
}

func TestTrayControllerLaunchAutoStart(t *testing.T) {
	ctx := context.Background()
	for _, tc := range []struct {
		name      string
		backend   fakeBackend
		wantCalls []string
		edited    int
	}{
		{"off", fakeBackend{status: control.Status{Phase: control.PhaseStopped}, check: validConfig}, []string{"check"}, 0},
		{"valid", fakeBackend{status: control.Status{Phase: control.PhaseStopped}, check: validConfig, autoStart: true}, []string{"check", "check", "login", "start"}, 0},
		{"already running", fakeBackend{status: control.Status{Phase: control.PhaseRunning, PID: 7}, check: validConfig, autoStart: true}, []string{"check"}, 0},
		{"unresponsive", fakeBackend{status: control.Status{Phase: control.PhaseUnresponsive, PID: 7}, check: validConfig, autoStart: true}, []string{"check"}, 0},
		{"invalid config", fakeBackend{status: control.Status{Phase: control.PhaseStopped}, check: missingHosts, autoStart: true}, []string{"check"}, 1},
		{"needs setup", fakeBackend{status: control.Status{Phase: control.PhaseStopped}, check: validConfig, autoStart: true, logins: []error{errLoginFailed, nil}},
			[]string{"check", "check", "login", "trust", "login", "start"}, 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			backend := tc.backend
			c, desktop, _ := newController(&backend)
			if job := c.Launch(ctx); job != nil {
				c.Finish(ctx, ui.ToggleProxy, job())
			}
			for range 3 {
				c.Refresh(ctx) // Status polls never repeat setup.
			}
			if !slices.Equal(backend.calls, tc.wantCalls) || len(desktop.edited) != tc.edited {
				t.Fatalf("calls %q (want %q), edited %q", backend.calls, tc.wantCalls, desktop.edited)
			}
		})
	}
}

func TestTrayControllerTogglesSettings(t *testing.T) {
	ctx := context.Background()
	backend := &fakeBackend{status: control.Status{Phase: control.PhaseConnecting, PID: 7}}
	c, desktop, view := newController(backend, ui.Script{Name: "Codex", Path: "Codex.ps1"})
	c.Refresh(ctx)
	if !view.item(ui.ScriptBase).Disabled || run(ctx, c, ui.ScriptBase) {
		t.Fatal("script ran before the proxy was ready")
	}
	backend.status.Phase = control.PhaseRunning
	c.Refresh(ctx)
	if view.item(ui.ScriptBase).Disabled || !run(ctx, c, ui.ScriptBase) || !slices.Equal(desktop.scripts, []string{"Codex"}) {
		t.Fatal("script unavailable while running", desktop.scripts)
	}
	if !run(ctx, c, ui.ToggleStartup) || !desktop.startup || !view.item(ui.ToggleStartup).Checked {
		t.Fatal("login startup toggle not applied", view.items)
	}
	for _, want := range []bool{true, false} {
		if !run(ctx, c, ui.ToggleAutoStart) || backend.autoStart != want || view.item(ui.ToggleAutoStart).Checked != want {
			t.Fatal("auto-start toggle not applied", want, view.items)
		}
	}
	if slices.Contains(backend.calls, "start") || slices.Contains(backend.calls, "stop") {
		t.Fatal("changing settings must not change the proxy", backend.calls)
	}
}

func TestTrayControllerBusyGateAndRecovery(t *testing.T) {
	ctx := context.Background()
	backend := &fakeBackend{status: control.Status{Phase: control.PhaseStopped}, check: validConfig, startErr: errors.New("start failed")}
	c, desktop, view := newController(backend)
	c.Refresh(ctx)
	job := c.Handle(ctx, ui.ToggleProxy)
	if job == nil || !view.item(ui.ToggleProxy).Disabled || !view.item(ui.ToggleStartup).Disabled || !view.item(ui.ToggleAutoStart).Disabled {
		t.Fatal("proxy action must disable mutations while it runs", view.items)
	}
	if c.Handle(ctx, ui.ToggleProxy) != nil || c.Handle(ctx, ui.ToggleStartup) != nil || c.Handle(ctx, ui.ToggleAutoStart) != nil {
		t.Fatal("second action accepted while busy")
	}
	if c.Handle(ctx, ui.OpenConfig) != nil || !slices.Equal(desktop.opened, []string{"config.toml"}) {
		t.Fatal("file actions stay available while busy", desktop.opened)
	}
	c.Finish(ctx, ui.ToggleProxy, job())
	c.Refresh(ctx)
	if !slices.Equal(desktop.messages, []string{"start failed"}) || view.item(ui.ToggleProxy).Disabled {
		t.Fatal("failed start must be reported once and leave Start available", desktop.messages, view.items)
	}
	backend.startErr, backend.calls = nil, nil
	if !run(ctx, c, ui.ToggleProxy) || !slices.Contains(backend.calls, "start") {
		t.Fatal("start not retried after a failure", backend.calls)
	}
	backend.calls = nil
	if !run(ctx, c, ui.ToggleProxy) || !slices.Equal(backend.calls, []string{"stop"}) {
		t.Fatal("running proxy not stopped", backend.calls)
	}
}

func TestTrayControllerStopAndExit(t *testing.T) {
	for _, stopErr := range []error{nil, errors.New("stop failed")} {
		ctx := context.Background()
		backend := &fakeBackend{status: control.Status{Phase: control.PhaseRunning, PID: 7}, stopErr: stopErr}
		c, desktop, view := newController(backend)
		c.Refresh(ctx)
		job := c.Handle(ctx, ui.StopAndQuit)
		if job == nil || !view.item(ui.Quit).Disabled || !view.item(ui.StopAndQuit).Disabled {
			t.Fatal("exit must wait for stop to complete", view.items)
		}
		if c.Handle(ctx, ui.ToggleProxy) != nil || c.Handle(ctx, ui.StopAndQuit) != nil {
			t.Fatal("another mutation was accepted while stopping")
		}
		if exit := c.Finish(ctx, ui.StopAndQuit, job()); exit != (stopErr == nil) {
			t.Fatal("exit must happen only after a successful stop", exit, stopErr)
		}
		if !slices.Equal(backend.calls, []string{"stop"}) {
			t.Fatal("exit must only stop the existing proxy", backend.calls)
		}
		c.Refresh(ctx)
		if stopErr != nil && (!slices.Equal(desktop.messages, []string{"stop failed"}) || view.item(ui.Quit).Disabled || view.item(ui.StopAndQuit).Disabled) {
			t.Fatal("failed stop must report the error and leave exit choices available", desktop.messages, view.items)
		}
	}
}

func TestTrayControllerOpensFailingConfigOnce(t *testing.T) {
	ctx := context.Background()
	backend := &fakeBackend{status: control.Status{Phase: control.PhaseStopped}, check: missingHosts}
	c, desktop, _ := newController(backend)
	for range 3 {
		if c.CheckConfig(ctx) {
			t.Fatal("invalid configuration accepted")
		}
	}
	if !slices.Equal(desktop.edited, []string{"ssh_config"}) || len(desktop.messages) != 3 {
		t.Fatal("failing file must open once while each check reports", desktop.edited, desktop.messages)
	}
	backend.check = config.ConfigCheck{Valid: false, EditFile: "config.toml", Error: "toml"}
	c.CheckConfig(ctx)
	if len(desktop.edited) != 1 {
		t.Fatal("invalid config.toml opens the log, not an editor", desktop.edited)
	}
}
