//go:build windows || darwin

package ui

import (
	"ssh_proxy/src/control"
	"strings"
)

const (
	ToggleProxy = 1 + iota
	ToggleStartup
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

// Tray-only phases: before the first status poll, and when polling fails.
const (
	phaseChecking control.Phase = "checking"
	phaseError    control.Phase = "error"
)

type Item struct {
	ID                           int
	Label                        string
	Checked, Disabled, Separator bool
}

// Menu is a snapshot of backend state, never a separate tray-owned proxy state.
func Menu(st control.Status, startup, startupKnown, busy bool, scripts ...Script) []Item {
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
		{ID: ToggleProxy, Label: toggle, Disabled: busy || st.Phase == phaseChecking || st.Phase == phaseError},
		{Separator: true},
	}
	for i, script := range scripts {
		items = append(items, Item{ID: ScriptBase + i, Label: "Run: " + script.Name, Disabled: busy || st.Phase != control.PhaseRunning})
	}
	if len(scripts) != 0 {
		items = append(items, Item{Separator: true})
	}
	return append(items, []Item{
		{ID: ToggleStartup, Label: "Launch App at Login", Checked: startup, Disabled: busy || !startupKnown},
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
