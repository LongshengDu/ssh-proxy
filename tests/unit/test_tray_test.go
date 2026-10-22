//go:build windows || darwin

package tests

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"ssh_proxy/src/proxy"
	"ssh_proxy/src/ui"
)

func TestConfigOpensOnce(t *testing.T) {
	seen := ui.SetupPrompts{}
	invalid := proxy.ConfigCheck{EditFile: "hosts", Error: "no hosts"}
	if seen.Config(invalid) != "hosts" || seen.Config(invalid) != "" || seen.Config(proxy.ConfigCheck{Valid: true, EditFile: "other"}) != "" {
		t.Fatal("configuration opener must run once for each invalid file")
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
		for _, phase := range []string{"stopped", "connecting", "degraded", "running"} {
			for _, busy := range []bool{false, true} {
				items := ui.Menu(proxy.Status{Phase: phase}, false, true, busy, scripts...)
				if !items[6].Separator || items[7].ID != ui.ScriptBase || items[7].Label != "Run: Codex" {
					t.Fatal(items)
				}
				if items[7].Disabled != (busy || phase != "running") {
					t.Fatal(phase, busy, items[7])
				}
			}
		}
	}
}

func TestTrayReflectsSupervisorAndStartupIndependently(t *testing.T) {
	for _, phase := range []string{"stopped", "connecting", "running", "degraded", "unresponsive"} {
		for _, startup := range []bool{false, true} {
			st := proxy.Status{Phase: phase, Destination: "tester@example.com:2222", Endpoint: "primary", SOCKS: "127.0.0.1:1081", HTTP: "127.0.0.1:8181"}
			control := "Start proxy"
			if phase != "stopped" {
				st.PID = 123
				control = "Stop proxy"
			}
			items := ui.Menu(st, startup, true, false)
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
				case ui.SSHLabel:
					if !strings.Contains(item.Label, st.Destination) || !item.Disabled {
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
	for _, item := range ui.Menu(proxy.Status{Phase: "running", PID: 123}, false, false, true) {
		if (item.ID == ui.ToggleProxy || item.ID == ui.ToggleStartup) && !item.Disabled {
			t.Fatal("mutation enabled during action", item)
		}
		if (item.ID == ui.OpenConfig || item.ID == ui.Quit) && item.Disabled {
			t.Fatal("recovery action unavailable", item)
		}
	}
	for _, item := range ui.Menu(proxy.Status{Phase: "error"}, false, true, false) {
		if item.ID == ui.ToggleProxy && !item.Disabled {
			t.Fatal("unknown backend status permits toggle")
		}
	}
}
