//go:build windows || darwin

package ui

import (
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"

	"ssh-proxy/src/platform"
)

type Script struct{ Name, Path string }

// Read once at tray startup; ReadDir supplies stable filename ordering.
func DiscoverScripts(directory, goos string) ([]Script, error) {
	extension := ".sh"
	if goos == "windows" {
		extension = ".ps1"
	}
	files, err := os.ReadDir(directory)
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var scripts []Script
	for _, file := range files {
		if !file.IsDir() && strings.EqualFold(filepath.Ext(file.Name()), extension) {
			scripts = append(scripts, Script{strings.TrimSuffix(file.Name(), filepath.Ext(file.Name())), filepath.Join(directory, file.Name())})
		}
	}
	return scripts, nil
}

func runScript(ctx context.Context, script Script, log io.Writer) error {
	cmd := exec.CommandContext(ctx, "/bin/bash", script.Path)
	if runtime.GOOS == "windows" {
		shell := filepath.Join(os.Getenv("SystemRoot"), "System32", "WindowsPowerShell", "v1.0", "powershell.exe")
		cmd = exec.CommandContext(ctx, shell, "-NoLogo", "-NoProfile", "-NonInteractive", "-ExecutionPolicy", "Bypass", "-File", script.Path)
	}
	cmd.Dir = filepath.Dir(script.Path)
	platform.HideChild(cmd)
	cmd.Stdout, cmd.Stderr = log, log
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("Run: %s: %w", script.Name, err)
	}
	return nil
}
