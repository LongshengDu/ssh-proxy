//go:build windows || darwin

package tests

import (
	"bytes"
	"context"
	"image/png"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"

	"ssh-proxy/src/control"
	"ssh-proxy/src/ui"
)

// Each size a tray draws has its own render, so no OS rescales the icon:
// Windows small icons for 100-350% scaling and the macOS menu bar at 2x.
// Other sizes use the next larger render, or the largest.
func TestTrayIconSizes(t *testing.T) {
	for _, tc := range []struct{ want, got int }{
		{16, 16}, {20, 20}, {24, 24}, {28, 28}, {32, 32}, {36, 36}, {40, 40}, {44, 44}, {48, 48}, {56, 56},
		{18, 20}, {100, 256}, {1000, 256},
	} {
		image, err := png.DecodeConfig(bytes.NewReader(ui.TrayIcon(tc.want)))
		if err != nil || image.Width != tc.got || image.Height != tc.got {
			t.Fatalf("tray icon for %d px: %dx%d, want %d (%v)", tc.want, image.Width, image.Height, tc.got, err)
		}
	}
}

func TestPlatformScriptsAndReadiness(t *testing.T) {
	dir := t.TempDir()
	for _, name := range []string{"Codex.ps1", "Codex.sh", "Two words.PS1", "notes.txt"} {
		if err := os.WriteFile(filepath.Join(dir, name), nil, 0600); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.Mkdir(filepath.Join(dir, "directory.ps1"), 0700); err != nil {
		t.Fatal(err)
	}
	for _, goos := range []string{"windows", "darwin"} {
		scripts, err := ui.DiscoverScripts(dir, goos)
		want := 1
		if goos == "windows" {
			want = 2
		}
		if err != nil || len(scripts) != want || scripts[0].Name != "Codex" {
			t.Fatal(goos, scripts, err)
		}
		items := ui.Menu(ui.MenuState{Status: control.Status{Phase: "running"}, StartupKnown: true, AutoStartKnown: true}, scripts...)
		index := slices.IndexFunc(items, func(item ui.Item) bool { return item.ID == ui.ScriptBase })
		if index < 1 || !items[index-1].Separator || items[index].Label != "Run: Codex" {
			t.Fatal(items)
		}
	}
}

// A process a script leaves running keeps the script's output handles. The
// script must still finish at once, or the tray stays busy and cannot exit.
func TestScriptLeavingProcessFinishes(t *testing.T) {
	dir := t.TempDir()
	script, text := filepath.Join(dir, "Leave.sh"), "echo started\nsleep 20 &\necho $! > pid\n"
	if runtime.GOOS == "windows" {
		script, text = filepath.Join(dir, "Leave.ps1"), "Write-Output started\n"+
			"$p = Start-Process -NoNewWindow -PassThru -FilePath \"$env:SystemRoot\\System32\\PING.EXE\" -ArgumentList '-n','20','127.0.0.1'\n"+
			"Set-Content -LiteralPath pid -Value $p.Id\n"
	}
	if err := os.WriteFile(script, []byte(text), 0600); err != nil {
		t.Fatal(err)
	}
	log, err := os.Create(filepath.Join(dir, "tray.txt"))
	if err != nil {
		t.Fatal(err)
	}
	defer log.Close()
	began := time.Now()
	err = ui.RunScript(context.Background(), ui.Script{Name: "Leave", Path: script}, log)
	elapsed := time.Since(began)
	data, _ := os.ReadFile(filepath.Join(dir, "pid"))
	if pid, _ := strconv.Atoi(strings.TrimSpace(string(data))); pid > 0 {
		if process, err := os.FindProcess(pid); err == nil {
			process.Kill()
			if runtime.GOOS == "windows" {
				process.Wait() // Release the log before the folder is removed.
			}
		}
	}
	output, _ := os.ReadFile(log.Name())
	if err != nil || elapsed > 10*time.Second || !strings.Contains(string(output), "started") {
		t.Fatalf("script took %s: %v; output %q", elapsed, err, output)
	}
}

func TestTrayReflectsSupervisorAndStartupIndependently(t *testing.T) {
	for _, phase := range []control.Phase{"stopped", "connecting", "running", "degraded", "unresponsive"} {
		for _, startup := range []bool{false, true} {
			st := control.Status{Phase: phase, Destination: "tester@example.com:2222", Endpoint: "primary", SOCKS: "127.0.0.1:1081", HTTP: "127.0.0.1:8181"}
			control := "Start proxy"
			if phase != "stopped" {
				st.PID = 123
				control = "Stop proxy"
			}
			items := ui.Menu(ui.MenuState{Status: st, Startup: startup, StartupKnown: true, AutoStart: !startup, AutoStartKnown: true})
			separators := 0
			for _, item := range items {
				if item.Separator {
					separators++
					continue
				}
				switch item.ID {
				case ui.ToggleProxy:
					if item.Label != control || item.Checked || item.Disabled {
						t.Fatal(phase, item)
					}
				case ui.ToggleStartup:
					if item.Checked != startup || item.Disabled {
						t.Fatal(phase, item)
					}
				case ui.ToggleAutoStart:
					if item.Checked == startup || item.Disabled {
						t.Fatal(phase, item)
					}
				case ui.StopAndQuit:
					if item.Disabled != (st.PID == 0) {
						t.Fatal("stop-and-exit must require a proxy process", phase, item)
					}
				case ui.Quit:
					want := "Exit app"
					if st.PID != 0 {
						want = "Exit app: Keep proxy running"
					}
					if item.Label != want || item.Disabled {
						t.Fatal("exit choice does not match proxy state", phase, item)
					}
				case ui.SSHLabel:
					if item.Label != "SSH: example.com:2222" || !item.Disabled {
						t.Fatal(item)
					}
				case ui.SOCKSLabel:
					if !strings.Contains(item.Label, st.SOCKS) {
						t.Fatal(item)
					}
				case ui.HTTPLabel:
					if !strings.Contains(item.Label, st.HTTP) {
						t.Fatal(item)
					}
				}
			}
			if separators != 5 {
				t.Fatal("expected five native separators", separators)
			}
		}
	}
	for _, item := range ui.Menu(ui.MenuState{Status: control.Status{Phase: "checking"}, StartupKnown: true}) {
		if item.ID == ui.ToggleProxy && !item.Disabled {
			t.Fatal("unknown backend status permits toggle")
		}
		if item.ID == ui.ToggleAutoStart && !item.Disabled {
			t.Fatal("unreadable auto-start setting permits toggle")
		}
	}
}
