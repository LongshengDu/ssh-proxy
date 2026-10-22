//go:build systemtest

package testutil

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"
)

func NativeLoginLaunch(t *testing.T, id, executable, config string) {
	t.Helper()
	home, err := os.UserHomeDir()
	if err != nil {
		t.Fatal(err)
	}
	label := "com.sshproxy.login." + id
	path := filepath.Join(home, "Library/LaunchAgents", label+".plist")
	for field, want := range map[string]string{"Label": label, "ProgramArguments.0": executable, "ProgramArguments.1": "--config", "ProgramArguments.2": config, "RunAtLoad": "true"} {
		out, err := exec.Command("/usr/bin/plutil", "-extract", field, "raw", "-o", "-", path).CombinedOutput()
		if err != nil || strings.TrimSpace(string(out)) != want {
			t.Fatalf("LaunchAgent %s: %v %s", field, err, out)
		}
	}
	domain := "gui/" + strconv.Itoa(os.Getuid())
	t.Cleanup(func() { exec.Command("/bin/launchctl", "bootout", domain+"/"+label).Run() })
	if out, err := exec.Command("/bin/launchctl", "bootstrap", domain, path).CombinedOutput(); err != nil {
		t.Fatalf("load isolated login LaunchAgent: %v %s", err, out)
	}
}

func apple(t *testing.T, script string) string {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	out, err := exec.CommandContext(ctx, "/usr/bin/osascript", "-e", script).CombinedOutput()
	if err != nil {
		t.Fatalf("native macOS automation failed: %v\n%s\nGrant this terminal/osascript Accessibility and Automation access in System Settings, then rerun.", err, out)
	}
	return strings.TrimSpace(string(out))
}
func NativeDesktop(t *testing.T) {
	t.Helper()
	if apple(t, `tell application "System Events" to get UI elements enabled`) != "true" {
		t.Fatal("macOS Accessibility permission is required for native system tests; enable it for the launching terminal and rerun")
	}
}

func NativeAcceptHost(t *testing.T, _ int, before map[string]bool, fingerprint string) {
	t.Helper()
	var window string
	WaitFor(t, func() bool {
		for id := range NativeTerminals(t) {
			if before[id] {
				continue
			}
			content := apple(t, `tell application "Terminal" to get contents of selected tab of window id `+id)
			if strings.Contains(strings.Join(strings.Fields(content), ""), fingerprint) && strings.Contains(content, "yes/no") {
				window = id
				return true
			}
		}
		return false
	})
	apple(t, `tell application "Terminal" to do script "yes" in selected tab of window id `+window)
}
func macMenu(pid int) string {
	return fmt.Sprintf(`tell application "System Events"
set p to first application process whose unix id is %d
tell p
set bar to last menu bar
set button to first menu bar item of bar
click button
delay 0.15
set popup to menu 1 of button
`, pid)
}
func NativeMenu(t *testing.T, pid int) []NativeItem {
	t.Helper()
	out := apple(t, macMenu(pid)+`
set resultText to ""
repeat with entry in menu items of popup
    set titleText to name of entry
    if titleText is missing value then set titleText to ""
    set checkedText to "false"
    try
        set mark to value of attribute "AXMenuItemMarkChar" of entry
        if mark is not missing value and mark is not "" then set checkedText to "true"
    end try
    set resultText to resultText & titleText & tab & (enabled of entry as text) & tab & checkedText & linefeed
end repeat
key code 53
return resultText
end tell
end tell`)
	var items []NativeItem
	for _, line := range strings.Split(out, "\n") {
		fields := strings.Split(strings.TrimSuffix(line, "\r"), "\t")
		if len(fields) != 3 {
			t.Fatalf("unrecognized native menu row %q", line)
		}
		items = append(items, NativeItem{fields[0], fields[1] != "true", fields[2] == "true", fields[0] == ""})
	}
	return items
}
func NativeClick(t *testing.T, pid int, label string) {
	t.Helper()
	WaitFor(t, func() bool {
		for _, item := range NativeMenu(t, pid) {
			if item.Label == label && !item.Disabled {
				return true
			}
		}
		return false
	})
	apple(t, macMenu(pid)+"click menu item "+strconv.Quote(label)+" of popup\nend tell\nend tell")
}

// Error dialogs are displayed by the app's osascript child, not by the tray PID.
func macDialog(t *testing.T, pid int, action string) string {
	t.Helper()
	out, err := exec.Command("/bin/ps", "-axo", "pid=,ppid=,comm=").Output()
	if err != nil {
		t.Fatal(err)
	}
	for _, line := range strings.Split(string(out), "\n") {
		f := strings.Fields(line)
		if len(f) < 3 || f[1] != strconv.Itoa(pid) || !strings.Contains(strings.Join(f[2:], " "), "osascript") {
			continue
		}
		return apple(t, `tell application "System Events"
set matches to application processes whose unix id is `+f[0]+`
if (count of matches) is 0 then return ""
tell item 1 of matches
    if (count of windows) is 0 then return ""
    set dialogWindow to window 1
`+action+`
end tell
end tell`)
	}
	return ""
}
func NativeError(t *testing.T, pid int) string {
	return macDialog(t, pid, `set resultText to name of dialogWindow & linefeed
repeat with entry in entire contents of dialogWindow
    try
        if role of entry is "AXStaticText" then set resultText to resultText & (value of entry as text) & linefeed
    end try
end repeat
return resultText`)
}
func NativeDismiss(t *testing.T, pid int) {
	t.Helper()
	macDialog(t, pid, `click button "OK" of dialogWindow`)
}
func NativeOpened(t *testing.T, path string) {
	t.Helper()
	name := filepath.Base(path)
	WaitFor(t, func() bool {
		out := apple(t, `tell application "System Events"
set resultText to ""
repeat with p in application processes
    try
        repeat with w in windows of p
            set resultText to resultText & (name of w as text) & linefeed
        end repeat
    end try
end repeat
return resultText
end tell`)
		return strings.Contains(out, name)
	})
}
func NativeTerminals(t *testing.T) map[string]bool {
	out := apple(t, `if application "Terminal" is not running then return ""
tell application "Terminal" to get id of every window`)
	result := map[string]bool{}
	for _, id := range strings.Split(out, ",") {
		if id = strings.TrimSpace(id); id != "" {
			result[id] = true
		}
	}
	return result
}
func NativeTerminalsClosed(t *testing.T, before map[string]bool) {
	t.Helper()
	WaitFor(t, func() bool {
		for id := range NativeTerminals(t) {
			if !before[id] {
				return false
			}
		}
		return true
	})
}
