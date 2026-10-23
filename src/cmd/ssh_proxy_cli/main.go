package main

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"ssh_proxy/src/cli"
	"ssh_proxy/src/platform"
)

func main() {
	if err := cli.Run(os.Args[1:]); err != nil {
		if len(os.Args) > 1 && os.Args[1] == "_run" && errors.Is(err, platform.ErrAlreadyRunning) {
			os.Exit(platform.AlreadyRunningExitCode)
		}
		var child *exec.ExitError
		if len(os.Args) > 1 && os.Args[1] == "_ssh" && errors.As(err, &child) {
			// SSH already wrote its diagnostic. Do not mask it with another line.
			code := child.ExitCode()
			if code < 0 {
				code = 1
			}
			os.Exit(code)
		}
		fmt.Fprintln(os.Stderr, "Error:", err)
		os.Exit(1)
	}
}
