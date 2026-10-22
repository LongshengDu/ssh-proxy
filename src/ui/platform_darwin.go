package ui

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

func defaultBackend(exe string) string {
	// Installed folder/ssh_proxy_app.app/Contents/MacOS/ssh_proxy_app
	return filepath.Clean(filepath.Join(filepath.Dir(exe), "..", "..", "..", "ssh_proxy_cli"))
}
func openPath(path string) error { return exec.Command("/usr/bin/open", path).Run() }

func RunTrustTerminal(ctx context.Context, backend, config, logPath string) error {
	quote := func(s string) string { return "'" + strings.ReplaceAll(s, "'", "'\"'\"'") + "'" }
	dir, err := os.MkdirTemp("", "ssh-proxy-trust-")
	if err != nil {
		return err
	}
	defer os.RemoveAll(dir)
	result := filepath.Join(dir, "exit-code")
	command := quote(backend) + " trust --log " + quote(logPath) + " --config " + quote(config)
	// Explicit sh also works when the user's Terminal profile uses another shell.
	command = "exec /bin/sh -c " + quote(command+"; code=$?; printf '%s' \"$code\" > "+quote(result)+"; exit \"$code\"")
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
tell application "Terminal"
    if exists setupWindow then close setupWindow saving no
end tell
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
