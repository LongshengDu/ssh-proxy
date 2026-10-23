//go:build windows || darwin

package main

import (
	"os"
	"runtime"
	"ssh-proxy/src/ui"
)

// AppKit requires the process's initial OS thread, including before main runs.
func init() { runtime.LockOSThread() }

func main() {
	if err := ui.Run(os.Args[1:]); err != nil {
		os.Exit(1)
	}
}
