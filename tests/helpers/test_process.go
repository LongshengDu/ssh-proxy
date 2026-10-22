package testutil

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"time"

	"golang.org/x/term"

	"ssh_proxy/src/cli"
	"ssh_proxy/src/config"
	"ssh_proxy/src/control"
)

// Child fixtures exercise the actual CLI/tray without a real SSH login.
func RunProcess() bool {
	// Terminal.app does not inherit the launching app's environment. Child-only
	// copies load their fixture settings from beside the copied test executable.
	if os.Getenv("SSH_PROXY_TEST_PROCESS") == "" {
		executable, _ := os.Executable()
		data, _ := os.ReadFile(filepath.Join(filepath.Dir(executable), "fixture.json"))
		var settings map[string]string
		if json.Unmarshal(data, &settings) == nil {
			for name, value := range settings {
				os.Setenv(name, value)
			}
		}
	}
	switch os.Getenv("SSH_PROXY_TEST_PROCESS") {
	case "trust":
		os.Setenv("SSH_PROXY_TEST_PROCESS", "ssh")
		if err := cli.Run(os.Args[1:]); err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
		return true
	case "ssh":
		if slices.Contains(os.Args, "-G") {
			fmt.Println("hostname fixture.invalid\nuser tester\nport 22")
			return true
		}
		if record := os.Getenv("SSH_PROXY_TEST_TERMINAL_RECORD"); record != "" {
			data, _ := json.Marshal([]bool{term.IsTerminal(int(os.Stdin.Fd())), term.IsTerminal(int(os.Stdout.Fd())), term.IsTerminal(int(os.Stderr.Fd()))})
			if err := os.WriteFile(record, data, 0600); err != nil {
				os.Exit(2)
			}
			if index := slices.Index(os.Args, "-E"); index >= 0 && index+1 < len(os.Args) {
				file, err := os.OpenFile(os.Args[index+1], os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0600)
				if err != nil {
					os.Exit(2)
				}
				fmt.Fprintln(file, "fixture SSH diagnostic")
				file.Close()
			} else {
				fmt.Fprintln(os.Stderr, "fixture SSH diagnostic")
			}
		}
		if slices.Contains(os.Args, os.Getenv("SSH_PROXY_TEST_SSH_SUCCESS")) {
			return true
		}
		time.Sleep(100 * time.Millisecond) // Allow the supervisor to attach containment.
		fmt.Fprintln(os.Stderr, os.Getenv("SSH_PROXY_TEST_DIAGNOSTIC"))
		os.Exit(255)
	case "cli":
		if len(os.Args) < 2 {
			os.Exit(1)
		}
		switch os.Args[1] {
		case "check":
			json.NewEncoder(os.Stdout).Encode(config.ConfigCheck{Valid: true})
		case "status":
			st := control.Status{Phase: "stopped"}
			if data, _ := os.ReadFile(os.Getenv("SSH_PROXY_TEST_RECORD")); strings.Contains(string(data), `"start"`) {
				st.PID, st.Phase = 123, "running"
			}
			json.NewEncoder(os.Stdout).Encode(st)
		case "trust", "start":
			file, err := os.OpenFile(os.Getenv("SSH_PROXY_TEST_RECORD"), os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0600)
			if err != nil {
				os.Exit(2)
			}
			json.NewEncoder(file).Encode(struct {
				Args                     []string
				Terminal, OutputTerminal bool
				PID                      int
			}{os.Args[1:], term.IsTerminal(int(os.Stdin.Fd())), term.IsTerminal(int(os.Stdout.Fd())), os.Getpid()})
			file.Close()
			// The silent check passes after an interactive setup exited 0, unless
			// SSH_PROXY_TEST_BATCH is "never" (password-only login) or "always".
			trusted := os.Getenv("SSH_PROXY_TEST_RECORD") + ".trusted"
			if os.Args[1] == "trust" && slices.Contains(os.Args, "--batch") {
				_, err := os.Stat(trusted)
				switch mode := os.Getenv("SSH_PROXY_TEST_BATCH"); {
				case mode == "always", mode != "never" && err == nil:
					os.Exit(0)
				}
				fmt.Fprintln(os.Stderr, "fixture: Permission denied (publickey)")
				os.Exit(255)
			}
			if os.Args[1] == "trust" {
				if gate := os.Getenv("SSH_PROXY_TEST_GATE"); gate != "" {
					deadline := time.Now().Add(90 * time.Second)
					for time.Now().Before(deadline) {
						if _, err := os.Stat(gate); err == nil {
							break
						}
						time.Sleep(50 * time.Millisecond)
					}
				}
				code, _ := strconv.Atoi(os.Getenv("SSH_PROXY_TEST_TRUST_EXIT"))
				if code == 0 {
					os.WriteFile(trusted, nil, 0600)
				}
				os.Exit(code)
			}
		}
		return true
	}
	return false
}
