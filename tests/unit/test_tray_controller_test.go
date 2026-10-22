//go:build windows || darwin

package tests

import (
	"context"
	"errors"
	"io"
	"log"
	"slices"
	"testing"

	"ssh_proxy/src/config"
	"ssh_proxy/src/control"
	"ssh_proxy/src/ui"
)

type fakeBackend struct {
	status                      control.Status
	check                       config.ConfigCheck
	trustErr, startErr, stopErr error
	calls                       []string
}

func (b *fakeBackend) Status() control.Status { b.calls = append(b.calls, "status"); return b.status }
func (b *fakeBackend) Check(context.Context) (config.ConfigCheck, error) {
	b.calls = append(b.calls, "check")
	return b.check, nil
}
func (b *fakeBackend) Trust(context.Context) error {
	b.calls = append(b.calls, "trust")
	return b.trustErr
}
func (b *fakeBackend) Start(context.Context) error {
	b.calls = append(b.calls, "start")
	return b.startErr
}
func (b *fakeBackend) Stop(context.Context) error {
	b.calls = append(b.calls, "stop")
	return b.stopErr
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

func TestTrayControllerBusyGateAndRecovery(t *testing.T) {
	ctx := context.Background()
	backend := &fakeBackend{status: control.Status{Phase: control.PhaseStopped}, check: config.ConfigCheck{Valid: true}, startErr: errors.New("start failed")}
	c, desktop, view := newController(backend)
	c.Refresh(ctx)
	job := c.Handle(ctx, ui.ToggleProxy)
	if job == nil || !view.item(ui.ToggleProxy).Disabled || !view.item(ui.ToggleStartup).Disabled {
		t.Fatal("proxy action must disable mutations while it runs", view.items)
	}
	if c.Handle(ctx, ui.ToggleProxy) != nil || c.Handle(ctx, ui.ToggleStartup) != nil {
		t.Fatal("second action accepted while busy")
	}
	if c.Handle(ctx, ui.OpenConfig) != nil || !slices.Equal(desktop.opened, []string{"config.toml"}) {
		t.Fatal("file actions stay available while busy", desktop.opened)
	}
	c.Finish(ctx, ui.ToggleProxy, job())
	c.Refresh(ctx)
	if !slices.Contains(desktop.messages, "start failed") || view.item(ui.ToggleProxy).Disabled {
		t.Fatal("failed start must be reported and leave Start available", desktop.messages, view.items)
	}
	if got := slices.DeleteFunc(slices.Clone(backend.calls), func(s string) bool { return s == "status" }); !slices.Equal(got, []string{"trust", "start", "check"}) {
		t.Fatal("unexpected backend calls", got)
	}
	backend.startErr, backend.calls = nil, nil
	if !run(ctx, c, ui.ToggleProxy) || !slices.Contains(backend.calls, "start") {
		t.Fatal("start not retried after a failure", backend.calls)
	}
	backend.status = control.Status{Phase: control.PhaseRunning, PID: 7}
	c.Refresh(ctx)
	backend.calls = nil
	if !run(ctx, c, ui.ToggleProxy) || !slices.Contains(backend.calls, "stop") {
		t.Fatal("running proxy not stopped", backend.calls)
	}
}

func TestTrayControllerTrustFailureSkipsStart(t *testing.T) {
	ctx := context.Background()
	backend := &fakeBackend{status: control.Status{Phase: control.PhaseStopped}, check: config.ConfigCheck{Valid: true}, trustErr: errors.New("SSH setup exited with code 7")}
	c, desktop, _ := newController(backend)
	c.Refresh(ctx)
	run(ctx, c, ui.ToggleProxy)
	if slices.Contains(backend.calls, "start") || !slices.Contains(desktop.messages, "SSH setup exited with code 7") {
		t.Fatal("failed trust must not start the proxy", backend.calls, desktop.messages)
	}
}

func TestTrayControllerOpensFailingConfigOnce(t *testing.T) {
	ctx := context.Background()
	backend := &fakeBackend{status: control.Status{Phase: control.PhaseStopped},
		check: config.ConfigCheck{Valid: false, EditFile: "ssh_config", Error: "add your hosts"}}
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

func TestTrayControllerScriptsNeedRunningProxy(t *testing.T) {
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
}
