package ui

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// trayIconSize: the menu bar draws the icon at 22 points; 44 pixels stay sharp on Retina.
func trayIconSize() int { return 44 }

func defaultBackend(exe string) string {
	// Installed folder/ssh-proxy-app.app/Contents/MacOS/ssh-proxy-app
	return filepath.Clean(filepath.Join(filepath.Dir(exe), "..", "..", "..", "ssh-proxy-cli"))
}
func openPath(path string) error {
	info, err := os.Stat(path)
	if err != nil {
		return err
	}
	args := []string{path}
	if !info.IsDir() {
		args = []string{"-t", path} // Configs and logs use the default text editor.
	}
	return exec.Command("/usr/bin/open", args...).Run()
}

func RunTrustTerminal(ctx context.Context, backend, logPath string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	quote := func(s string) string { return "'" + strings.ReplaceAll(s, "'", "'\"'\"'") + "'" }
	dir, err := os.MkdirTemp("", "ssh-proxy-login-")
	if err != nil {
		return err
	}
	defer os.RemoveAll(dir)
	result := filepath.Join(dir, "exit-code")
	command := quote(backend) + " login --log " + quote(logPath)
	// Explicit sh also works when the user's Terminal profile uses another shell.
	command = "exec /bin/sh -c " + quote(command+"; code=$?; printf '%s' \"$code\" > "+quote(result+".tmp")+" && mv "+quote(result+".tmp")+" "+quote(result)+"; exit \"$code\"")
	out, err := exec.CommandContext(ctx, "/usr/bin/osascript", "-e", `on run argv
tell application "Terminal"
    set setupTab to do script (item 1 of argv)
    set setupWindow to front window
    activate
end tell
repeat
    try
        set exitCode to do shell script "/bin/cat " & quoted form of (item 2 of argv)
        exit repeat
    end try
    tell application "Terminal"
        if not (exists setupTab) then error "SSH setup terminal closed before completion."
    end tell
    delay 0.2
end repeat
-- A failed setup leaves its window open so its messages stay readable.
if exitCode is "0" then
    tell application "Terminal"
        if exists setupWindow then close setupWindow saving no
    end tell
end if
return exitCode
end run`, command, result).CombinedOutput()
	if err != nil {
		return fmt.Errorf("SSH setup terminal: %w: %s", err, out)
	}
	if strings.TrimSpace(string(out)) != "0" {
		return fmt.Errorf("SSH setup exited with code %s", strings.TrimSpace(string(out)))
	}
	return nil
}

func errorMessage(message string) error {
	// Pass text as data, never interpolate host names or errors into AppleScript.
	return exec.Command("/usr/bin/osascript", "-e", `on run argv
display dialog (item 1 of argv) with title "SSH Proxy — Error" buttons {"OK"} default button "OK" with icon stop
end run`, message).Run()
}
