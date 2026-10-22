//go:build windows || darwin

package ui

import (
	"ssh_proxy/src/control"
	"strings"
)

const (
	ToggleProxy = 1 + iota
	ToggleStartup
	ToggleAutoStart
	OpenLog
	OpenConfig
	OpenSSHConfig
	OpenLocation
	Quit
	StatusLabel
	SSHLabel
	SOCKSLabel
	HTTPLabel
	ScriptBase
)

// phaseChecking is the tray-only phase before the first status read.
const phaseChecking control.Phase = "checking"

type Item struct {
	ID                           int
	Label                        string
	Checked, Disabled, Separator bool
}

// MenuState is everything the menu shows. Known is false when a setting
// cannot be read, which disables its checkbox.
type MenuState struct {
	Status                    control.Status
	Startup, StartupKnown     bool // Launch App at Login.
	AutoStart, AutoStartKnown bool // app.start_proxy_on_launch in config.toml.
	Busy                      bool
}

// Menu is a snapshot of backend state, never a separate tray-owned proxy state.
func Menu(s MenuState, scripts ...Script) []Item {
	st, busy := s.Status, s.Busy
	if st.Phase == "" {
		st.Phase = phaseChecking
	}
	label := "○ " + strings.ToUpper(string(st.Phase[:1])) + string(st.Phase[1:])
	if st.Phase == control.PhaseRunning {
		label = "● Running"
	}
	if busy {
		label += " — working…"
	}
	value := func(s string) string {
		if s == "" {
			return "—"
		}
		return s
	}
	ssh := value(st.Destination)
	if st.Endpoint != "" {
		ssh += " (" + st.Endpoint + ")"
	}
	toggle := "Start proxy"
	if st.PID != 0 {
		toggle = "Stop proxy"
	}
	items := []Item{
		{ID: StatusLabel, Label: label, Disabled: true},
		{ID: SSHLabel, Label: "SSH: " + ssh, Disabled: true},
		{ID: SOCKSLabel, Label: "SOCKS5: " + value(st.SOCKS), Disabled: true},
		{ID: HTTPLabel, Label: "HTTP: " + value(st.HTTP), Disabled: true},
		{Separator: true},
		{ID: ToggleProxy, Label: toggle, Disabled: busy || st.Phase == phaseChecking},
		{Separator: true},
	}
	for i, script := range scripts {
		items = append(items, Item{ID: ScriptBase + i, Label: "Run: " + script.Name, Disabled: busy || st.Phase != control.PhaseRunning})
	}
	if len(scripts) != 0 {
		items = append(items, Item{Separator: true})
	}
	return append(items, []Item{
		{ID: ToggleStartup, Label: "Launch App at Login", Checked: s.Startup, Disabled: busy || !s.StartupKnown},
		{ID: ToggleAutoStart, Label: "Start Proxy When App Launches", Checked: s.AutoStart, Disabled: busy || !s.AutoStartKnown},
		{Separator: true},
		{ID: OpenSSHConfig, Label: "Open SSH Config"},
		{ID: OpenConfig, Label: "Open App Config"},
		{Separator: true},
		{ID: OpenLog, Label: "Open Current Log"},
		{ID: OpenLocation, Label: "Open Installed Location"},
		{Separator: true},
		{ID: Quit, Label: "Exit app (proxy keeps running)"},
	}...)
}
