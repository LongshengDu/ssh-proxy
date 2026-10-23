package platform

import (
	"bytes"
	"encoding/xml"
	"fmt"
	"strconv"
	"strings"
)

// LoginEntry registers a command for the current user's next login, without
// starting or stopping it. Name identifies the OS entry; Key isolates Windows
// long-path settings. Status reports registration, not external OS overrides.
type LoginEntry struct {
	Name, Key           string
	Command             []string
	AbandonProcessGroup bool // macOS: preserve independently managed children when this app exits.
}

// LaunchAgent and SystemdUnit are kept platform independent for format tests.
func (e LoginEntry) LaunchAgent() string {
	esc := func(s string) string { var b bytes.Buffer; _ = xml.EscapeText(&b, []byte(s)); return b.String() }
	var args strings.Builder
	for _, arg := range e.Command {
		fmt.Fprintf(&args, "<string>%s</string>", esc(arg))
	}
	policy := ""
	if e.AbandonProcessGroup {
		policy = "<key>AbandonProcessGroup</key><true/>"
	}
	return `<?xml version="1.0" encoding="UTF-8"?><!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd"><plist version="1.0"><dict><key>Label</key><string>` + esc(e.Name) + `</string><key>ProgramArguments</key><array>` + args.String() + `</array><key>RunAtLoad</key><true/><key>KeepAlive</key><false/>` + policy + `</dict></plist>`
}

func SystemdUnit(command []string) string {
	args := make([]string, len(command))
	for i, arg := range command {
		args[i] = strconv.Quote(strings.NewReplacer("%", "%%", "$", "$$").Replace(arg))
	}
	return "[Unit]\nDescription=SSH Proxy\n\n[Service]\nExecStart=" + strings.Join(args, " ") + "\nRestart=no\nTimeoutStopSec=25\nKillMode=control-group\nUMask=0077\n\n[Install]\nWantedBy=default.target\n"
}
