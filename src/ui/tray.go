//go:build windows || darwin

package ui

import (
	"bytes"
	"context"
	"embed"
	"encoding/json"
	"fmt"
	"log"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/gogpu/systray"

	"ssh-proxy/src/config"
	"ssh-proxy/src/control"
	"ssh-proxy/src/logs"
	"ssh-proxy/src/platform"
)

//go:embed icons
var icons embed.FS

// TrayIcon returns the icon rendered for size pixels, so the OS draws it
// without rescaling. Lacking that size, it returns the smallest larger render,
// or the largest one. scripts/render-icons.py renders the set from icon.svg.
func TrayIcon(size int) []byte {
	entries, _ := icons.ReadDir("icons")
	best := 0
	for _, entry := range entries {
		var n int
		if _, err := fmt.Sscanf(entry.Name(), "icon-%d.png", &n); err != nil {
			continue
		}
		if best == 0 || (n >= size && (best < size || n < best)) || (best < size && n > best) {
			best = n
		}
	}
	data, _ := icons.ReadFile(fmt.Sprintf("icons/icon-%d.png", best))
	return data
}

// run delegates the icon, native menu and event loop entirely to
// gogpu/systray. One worker goroutine owns the Controller and serializes actions.
func run(p config.Paths, backend string, logger *log.Logger, writer *logs.RunLog) error {
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()
	scripts, err := DiscoverScripts(filepath.Join(filepath.Dir(backend), "scripts"), runtime.GOOS)
	if err != nil {
		return err
	}

	tray := systray.New()
	view := &menuView{tray: tray, items: map[int]*systray.MenuItem{}, shown: map[int]Item{}}
	menu := systray.NewMenu()
	actions := make(chan int, 1)
	for _, item := range Menu(MenuState{Status: control.Status{Phase: phaseChecking}}, scripts...) {
		if item.Separator {
			menu.AddSeparator()
			continue
		}
		click := func() {
			// Checkboxes reflect confirmed state, not an optimistic toggle.
			if view.items[item.ID].IsDisabled() {
				return
			}
			select {
			case actions <- item.ID:
			default:
			}
		}
		if item.ID == ToggleStartup || item.ID == ToggleAutoStart {
			view.items[item.ID] = menu.AddCheckbox(item.Label, item.Checked, click)
		} else {
			view.items[item.ID] = menu.Add(item.Label, click)
		}
		view.items[item.ID].SetDisabled(item.Disabled)
		view.shown[item.ID] = item
	}
	view.tooltip = "SSH Proxy - right-click for status and controls"
	tray.SetIcon(TrayIcon(trayIconSize())).SetTooltip(view.tooltip).SetMenu(menu).Show()
	defer tray.Remove()
	// Bounds verifies that Shell_NotifyIcon succeeded; the library's fluent
	// Show method intentionally does not return native initialization errors.
	if runtime.GOOS == "windows" {
		_, _, width, height := tray.Bounds()
		if width == 0 || height == 0 {
			return fmt.Errorf("Windows notification area unavailable; see %s", writer.Name())
		}
	}
	ctx, cancel := context.WithCancel(context.Background())
	desktop := &desktopOS{id: p.ID, log: writer}
	controller := NewController(cliBackend{paths: p, exe: backend, log: writer}, desktop, view, logger, p, filepath.Dir(backend), scripts)
	var worker sync.WaitGroup
	worker.Add(1)
	go func() {
		defer worker.Done()
		ticker := time.NewTicker(2 * time.Second)
		defer ticker.Stop()
		type result struct {
			id  int
			err error
		}
		completed := make(chan result, 1)
		start := func(id int, job func() error) {
			worker.Add(1)
			go func() {
				defer worker.Done()
				completed <- result{id, job()}
			}()
		}
		if job := controller.Launch(ctx); job != nil {
			start(ToggleProxy, job)
		}
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
			case r := <-completed:
				if controller.Finish(ctx, r.id, r.err) {
					tray.Remove()
					return
				}
			case id := <-actions:
				if id == Quit && controller.action != StopAndQuit {
					logger.Print("Exiting app; leaving proxy unchanged")
					tray.Remove()
					return
				}
				if job := controller.Handle(ctx, id); job != nil {
					start(id, job)
				}
			}
			controller.Refresh(ctx)
		}
	}()
	err = tray.Run()
	cancel()
	worker.Wait()
	return err
}

// menuView updates only changed native items: the library sends every setter
// to the OS, including no-ops, which makes an open menu flicker.
type menuView struct {
	tray    *systray.SystemTray
	items   map[int]*systray.MenuItem
	shown   map[int]Item
	tooltip string
}

func (v *menuView) Render(items []Item, tooltip string) {
	for _, item := range items {
		if item.Separator {
			continue
		}
		entry, previous := v.items[item.ID], v.shown[item.ID]
		if item.Label != previous.Label {
			entry.SetLabel(item.Label)
		}
		if item.Checked != previous.Checked {
			entry.SetChecked(item.Checked)
		}
		if item.Disabled != previous.Disabled {
			entry.SetDisabled(item.Disabled)
		}
		v.shown[item.ID] = item
	}
	if tooltip != v.tooltip {
		v.tray.SetTooltip(tooltip)
		v.tooltip = tooltip
	}
}

// desktopOS is the tray's file, login, script and dialog integration.
type desktopOS struct {
	id           string
	log          *logs.RunLog
	showingError atomic.Bool
}

func (d *desktopOS) StartupEnabled() (bool, error) { return StartupEnabled(d.id) }
func (d *desktopOS) SetStartup(enabled bool) error {
	exe, err := os.Executable()
	if err != nil {
		return err
	}
	return SetStartup(d.id, exe, enabled)
}
func (d *desktopOS) Open(path string) error         { return openPath(path) }
func (d *desktopOS) OpenEditable(path string) error { return openEditable(path) }
func (d *desktopOS) RunScript(ctx context.Context, script Script) error {
	// The script appends to the tray log directly; defer size cuts meanwhile.
	release := d.log.Hold()
	defer release()
	return RunScript(ctx, script, d.log.File)
}

// ShowError never blocks the caller, so a visible error cannot freeze status
// refresh; further errors while one dialog is open are only logged.
func (d *desktopOS) ShowError(err error) {
	d.ShowProxyError(err, d.log.Name())
}

func (d *desktopOS) ShowProxyError(err error, logPath string) {
	if d.showingError.CompareAndSwap(false, true) {
		go func() {
			defer d.showingError.Store(false)
			showError(err, logPath)
		}()
	}
}

// cliBackend runs validation, login setup and lifecycle commands through the CLI and
// reads status in-process from the supervisor's authenticated control API.
type cliBackend struct {
	paths config.Paths
	exe   string
	log   *logs.RunLog // The tray log that interactive trust appends to.
}

func (b cliBackend) Status() control.Status { return control.QueryStatus(b.paths) }
func (b cliBackend) Check(ctx context.Context) (config.ConfigCheck, error) {
	var result config.ConfigCheck
	out, err := commandBackend(ctx, b.exe, "check")
	if err == nil {
		err = json.Unmarshal(out, &result)
	}
	return result, err
}
func (b cliBackend) CheckLogin(ctx context.Context) error {
	_, err := commandBackend(ctx, b.exe, "login", "--check")
	return err
}
func (b cliBackend) Trust(ctx context.Context) error {
	// The login CLI and native ssh -E append to the tray log meanwhile; a cut
	// during their writes would lose them.
	release := b.log.Hold()
	defer release()
	err := RunTrustTerminal(ctx, b.exe, b.paths.Log)
	if ctx.Err() != nil {
		// Closing the app leaves the user's setup terminal alive. Its log writes
		// can outlive this call, so do not resume cuts during app shutdown.
		b.log.SetLimit(0)
	}
	return err
}
func (b cliBackend) Start(ctx context.Context) error {
	_, err := commandBackend(ctx, b.exe, "start")
	return err
}
func (b cliBackend) Stop(ctx context.Context) error {
	_, err := commandBackend(ctx, b.exe, "stop")
	return err
}

func (b cliBackend) AutoStart() (bool, error) {
	s, err := config.LoadAppSettings(b.paths.App)
	return s.StartProxyOnLaunch, err
}
func (b cliBackend) SetAutoStart(enabled bool) error {
	return config.SaveAppSettings(b.paths.App, config.AppSettings{StartProxyOnLaunch: enabled})
}

func commandBackend(parent context.Context, backend string, args ...string) ([]byte, error) {
	ctx, cancel := context.WithTimeout(parent, 90*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, backend, args...)
	platform.HideChild(cmd)
	var output bytes.Buffer
	cmd.Stdout, cmd.Stderr = &output, &output
	var err error
	if args[0] == "start" {
		err = cmd.Run() // The detached supervisor must outlive this command.
	} else {
		// Closing the app or a timeout also ends a check's ssh child, which would
		// otherwise keep running and hold the output pipe open.
		err = platform.RunTree(cmd)
	}
	out := output.Bytes()
	slog.Info("CLI command", "args", args, "output", strings.TrimSpace(string(out)))
	if err != nil {
		return nil, fmt.Errorf("ssh-proxy-cli %s: %w: %s", strings.Join(args, " "), err, strings.TrimSpace(string(out)))
	}
	return out, nil
}
