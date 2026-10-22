//go:build windows || darwin

package ui

import (
	"context"
	_ "embed"
	"encoding/json"
	"errors"
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

	"github.com/BurntSushi/toml"
	"github.com/gogpu/systray"
	"ssh_proxy/src/proxy"
)

//go:embed icon.png
var icon []byte

// run delegates the icon, native menu and event loop entirely
// to gogpu/systray. One worker serializes CLI actions and backend state refresh.
func run(p proxy.Paths, backend string, smoke bool, logger *log.Logger, writer *proxy.RunLog) error {
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()
	unlock, err := proxy.LockInstance(p.ID + "-tray")
	if err != nil {
		return err
	}
	defer unlock()
	scripts, err := DiscoverScripts(filepath.Join(filepath.Dir(backend), "scripts"), runtime.GOOS)
	if err != nil {
		return err
	}

	tray := systray.New()
	menu := systray.NewMenu()
	items := map[int]*systray.MenuItem{}
	shown := map[int]Item{}
	actions := make(chan int, 1)
	for _, item := range Menu(proxy.Status{Phase: phaseChecking}, false, false, false, scripts...) {
		if item.Separator {
			menu.AddSeparator()
			continue
		}
		click := func() {
			// Checkboxes reflect confirmed backend state, not an optimistic toggle.
			if items[item.ID].IsDisabled() {
				return
			}
			select {
			case actions <- item.ID:
			default:
			}
		}
		if item.ID == ToggleStartup {
			items[item.ID] = menu.AddCheckbox(item.Label, item.Checked, click)
		} else {
			items[item.ID] = menu.Add(item.Label, click)
		}
		items[item.ID].SetDisabled(item.Disabled)
		shown[item.ID] = item
	}
	tray.SetIcon(icon).SetTooltip("SSH Proxy - right-click for status and controls").SetMenu(menu).Show()
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
	// Dialogs run independently so a visible error never freezes status refresh.
	var showingError atomic.Bool
	report := func(err error) {
		logger.Print(err)
		if showingError.CompareAndSwap(false, true) {
			go func() {
				defer showingError.Store(false)
				showError(err, writer.Name())
			}()
		}
	}
	var worker sync.WaitGroup
	worker.Add(1)
	go func() {
		defer worker.Done()
		ticker := time.NewTicker(2 * time.Second)
		defer ticker.Stop()
		var smokeTimer <-chan time.Time
		if smoke {
			timer := time.NewTimer(1500 * time.Millisecond)
			defer timer.Stop()
			smokeTimer = timer.C
		}
		st := proxy.Status{Phase: phaseChecking}
		setup := SetupPrompts{}
		var startup bool
		var startupErr error
		busy := false
		type actionResult struct {
			id  int
			err error
		}
		completed := make(chan actionResult, 1)
		tooltip := "SSH Proxy - right-click for status and controls"
		render := func() {
			for _, item := range Menu(st, startup, startupErr == nil, busy, scripts...) {
				if item.Separator {
					continue
				}
				entry, previous := items[item.ID], shown[item.ID]
				// The library dispatches every setter to the OS, including no-ops.
				if item.Label != previous.Label {
					entry.SetLabel(item.Label)
				}
				if item.Checked != previous.Checked {
					entry.SetChecked(item.Checked)
				}
				if item.Disabled != previous.Disabled {
					entry.SetDisabled(item.Disabled)
				}
				shown[item.ID] = item
			}
			if next := "SSH Proxy — " + string(st.Phase); next != tooltip {
				tray.SetTooltip(next)
				tooltip = next
			}
		}
		// Configuration validation belongs to the CLI, including native ssh -G.
		checkConfig := func() {
			out, err := commandBackend(ctx, p, backend, "check")
			var result proxy.ConfigCheck
			if err == nil {
				err = json.Unmarshal(out, &result)
			}
			if err == nil && !result.Valid {
				err = errors.New(result.Error)
				if path := setup.Config(result); path != "" && path != p.Config {
					logger.Printf("Opening configuration for setup: %s", path)
					err = errors.Join(err, openEditable(path))
				}
			}
			if err != nil && ctx.Err() == nil {
				report(err)
			}
		}
		checkConfig()
		lastError := ""
		refresh := func() {
			previous := st
			out, err := commandBackend(ctx, p, backend, "status")
			st = proxy.Status{}
			if err == nil {
				err = json.Unmarshal(out, &st)
			}
			if err != nil {
				st = proxy.Status{Phase: phaseError, LastError: err.Error()}
			}
			startup, startupErr = StartupEnabled(p.ID)
			issue := errors.Join(err, startupErr)
			if err == nil && st.LastError != "" {
				issue = errors.Join(issue, errors.New(st.LastError))
			}
			message := ""
			if issue != nil {
				message = issue.Error()
			}
			if message != "" && message != lastError && ctx.Err() == nil {
				report(issue)
			}
			lastError = message
			if st.Phase != previous.Phase || st.PID != previous.PID || st.Endpoint != previous.Endpoint {
				logger.Printf("Proxy status: %s; pid=%d; endpoint=%s; SOCKS5=%s; HTTP=%s", st.Phase, st.PID, st.Destination, st.SOCKS, st.HTTP)
			}
			render()
		}
		refresh()
		for {
			select {
			case <-ctx.Done():
				return
			case <-smokeTimer:
				tray.Remove()
				return
			case <-ticker.C:
			case result := <-completed:
				busy = false
				if result.err != nil && ctx.Err() == nil {
					report(result.err)
					if result.id == ToggleProxy {
						checkConfig()
					}
				}
			case a := <-actions:
				if a == Quit {
					tray.Remove()
					return
				}
				if a >= OpenLog && a <= OpenLocation {
					if err := perform(ctx, p, backend, a, st.PID != 0, startup); err != nil {
						report(err)
					}
					continue
				}
				if busy || (a >= ScriptBase && st.Phase != proxy.PhaseRunning) {
					continue
				}
				busy = true
				render()
				worker.Add(1)
				go func(id int, active, startup bool) {
					defer worker.Done()
					var err error
					if id >= ScriptBase && id-ScriptBase < len(scripts) {
						script := scripts[id-ScriptBase]
						logger.Printf("Run: %s (%s)", script.Name, script.Path)
						err = runScript(ctx, script, writer)
					} else {
						err = perform(ctx, p, backend, id, active, startup)
					}
					completed <- actionResult{id, err}
				}(a, st.PID != 0, startup)
			}
			refresh()
		}
	}()
	err = tray.Run()
	cancel()
	worker.Wait()
	return err
}

func perform(ctx context.Context, p proxy.Paths, backend string, id int, active, startup bool) error {
	switch id {
	case ToggleProxy:
		command := "start"
		if active {
			command = "stop"
		}
		args := []string{command}
		if !active {
			slog.Info("Running SSH trust before proxy start")
			if err := RunTrustTerminal(ctx, backend, p.Config, p.Log); err != nil {
				return err
			}
			if err := ctx.Err(); err != nil {
				return err
			}
			args = append(args, "--wait", "1")
		}
		_, err := commandBackend(ctx, p, backend, args...)
		return err
	case ToggleStartup:
		exe, err := os.Executable()
		if err != nil {
			return err
		}
		return SetStartup(p.ID, exe, p.Config, !startup)
	case OpenLog:
		return openPath(p.Log)
	case OpenConfig:
		return openPath(p.Config)
	case OpenSSHConfig:
		// Only decode the path: editing must work even if SSH is unavailable.
		var c proxy.Config
		if _, err := toml.DecodeFile(p.Config, &c); err != nil {
			return err
		}
		path := c.SSH.ConfigFile
		if path == "" {
			return fmt.Errorf("ssh.config_file is missing in config.toml")
		}
		if strings.HasPrefix(path, "~/") || strings.HasPrefix(path, "~\\") {
			home, err := os.UserHomeDir()
			if err != nil {
				return err
			}
			path = filepath.Join(home, path[2:])
		}
		if !filepath.IsAbs(path) {
			path = filepath.Join(filepath.Dir(p.Config), path)
		}
		return openEditable(path)
	case OpenLocation:
		return openPath(filepath.Dir(backend))
	}
	return nil
}

func commandBackend(parent context.Context, p proxy.Paths, backend string, args ...string) ([]byte, error) {
	timeout := 90 * time.Second
	if args[0] == "status" {
		timeout = 5 * time.Second
	}
	ctx, cancel := context.WithTimeout(parent, timeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, backend, append(args, "--config", p.Config)...)
	proxy.HideChild(cmd)
	out, err := cmd.CombinedOutput()
	if args[0] != "status" {
		slog.Info("CLI command", "args", args, "output", strings.TrimSpace(string(out)))
	}
	if err != nil {
		return nil, fmt.Errorf("ssh_proxy_cli %s: %w: %s", strings.Join(args, " "), err, strings.TrimSpace(string(out)))
	}
	return out, nil
}
