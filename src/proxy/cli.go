package proxy

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"os/signal"
	"path/filepath"
	"slices"
	"strings"
	"time"
)

// scripts/build.py sets this from the repository's .version file.
var version = "dev"

func TakeConfig(args []string) (string, []string, error) {
	var config string
	rest := []string{}
	for i := 0; i < len(args); i++ {
		if args[i] == "--config" || args[i] == "-c" {
			i++
			if i >= len(args) {
				return "", nil, fmt.Errorf("--config needs a path")
			}
			config = args[i]
		} else if strings.HasPrefix(args[i], "--config=") || strings.HasPrefix(args[i], "-c=") {
			_, config, _ = strings.Cut(args[i], "=")
			if config == "" {
				return "", nil, fmt.Errorf("--config needs a path")
			}
		} else {
			rest = append(rest, args[i])
		}
	}
	return config, rest, nil
}
func printJSON(value any) error {
	encoder := json.NewEncoder(os.Stdout)
	encoder.SetIndent("", "  ")
	return encoder.Encode(value)
}
func printStatus(st Status) error {
	fmt.Printf("Proxy: %s\n", st.Phase)
	if st.Destination != "" {
		fmt.Printf("SSH: %s (%s)\nSOCKS5: %s\nHTTP: %s\n", st.Destination, st.Endpoint, st.SOCKS, st.HTTP)
	}
	if st.LastError != "" {
		fmt.Printf("Last error: %s\n", st.LastError)
	}
	_, err := fmt.Printf("Log: %s\n", st.Log)
	return err
}

func testProxy(c *Config, target string) error {
	u, err := url.Parse(target)
	if err != nil || u.Host == "" || (u.Scheme != "http" && u.Scheme != "https") {
		return fmt.Errorf("test URL must use http:// or https://")
	}
	dial, err := socksDialer(c.socksAddr(), 10*time.Second)
	if err != nil {
		return err
	}
	proxyURL := &url.URL{Scheme: "http", Host: c.httpAddr()}
	fmt.Printf("Testing %s\n", target)
	allOK := true
	for _, test := range []struct {
		name      string
		transport *http.Transport
	}{
		{"HTTP proxy", &http.Transport{Proxy: http.ProxyURL(proxyURL), TLSHandshakeTimeout: 15 * time.Second}},
		{"SOCKS5", &http.Transport{Proxy: nil, DialContext: dial, TLSHandshakeTimeout: 15 * time.Second}},
	} {
		client := &http.Client{Transport: test.transport, Timeout: 30 * time.Second}
		ok := false
		var detail string
		resp, err := client.Get(target)
		if err != nil {
			detail = err.Error()
		} else {
			_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 1024*1024))
			resp.Body.Close()
			detail, ok = resp.Status, resp.StatusCode < 400
		}
		test.transport.CloseIdleConnections()
		label := "FAIL"
		if ok {
			label = "PASS"
		}
		fmt.Printf("%s %s: %s\n", label, test.name, detail)
		allOK = allOK && ok
	}
	if !allOK {
		return fmt.Errorf("proxy checks failed")
	}
	return nil
}

// Run executes one CLI command.
func Run(args []string) error {
	if len(args) > 0 && args[0] == "_ssh" {
		return runSSHGuardian(args[1:])
	}
	config, args, err := TakeConfig(args)
	if err != nil {
		return err
	}
	if len(args) == 0 || args[0] == "--help" || args[0] == "-h" {
		return writeHelp(os.Stdout, "", nil)
	}
	if args[0] == "-v" || args[0] == "--version" {
		if len(args) != 1 {
			return fmt.Errorf("--version takes no arguments")
		}
		_, err := fmt.Printf("ssh_proxy_cli %s\n", version)
		return err
	}
	if args[0] == "--foreground" {
		args = append([]string{"start"}, args...)
	}
	command := args[0]
	if command == "service" && len(args) > 1 && !strings.HasPrefix(args[1], "-") {
		command += " " + args[1]
	}
	f, options := commandFlags(command)
	if slices.Contains(args, "--help") || slices.Contains(args, "-h") {
		return writeHelp(os.Stdout, command, f)
	}
	p, err := PathsFor(config)
	if err != nil {
		return err
	}
	if handled, err := serviceCommand(p, args); handled {
		return err
	}
	switch args[0] {
	case "start", "restart":
		foreground, wait := &options.foreground, &options.wait
		if err = f.Parse(args[1:]); err != nil {
			return err
		}
		if f.NArg() != 0 || *wait < 1 || *wait > 3600 {
			return fmt.Errorf("invalid start arguments")
		}
		// Validate once before touching a service or a running instance; _run validates again.
		if !*foreground || args[0] == "restart" {
			if _, err = LoadConfig(p.Config); err != nil {
				return err
			}
		}
		if !*foreground {
			handled, e := serviceAction(p, args[0], time.Duration(*wait)*time.Second)
			if e != nil {
				return e
			}
			if handled {
				return printStatus(QueryStatus(p))
			}
		}
		if args[0] == "restart" {
			if *foreground {
				if _, err = serviceAction(p, "stop", 0); err != nil {
					return err
				}
			}
			if err = stopBackground(p); err != nil {
				return err
			}
		}
		if *foreground {
			err = runSupervisor(p, true)
		} else {
			err = startBackground(p, time.Duration(*wait)*time.Second)
		}
		if err != nil {
			return err
		}
		return printStatus(QueryStatus(p))
	case "_run":
		if len(args) != 1 {
			return fmt.Errorf("invalid internal arguments")
		}
		return runSupervisor(p, false)
	case "stop":
		if len(args) != 1 {
			return fmt.Errorf("stop takes no options")
		}
		handled, err := serviceAction(p, "stop", 0)
		if err != nil {
			return err
		}
		if !handled {
			if err := stopBackground(p); err != nil {
				return err
			}
		}
		return printStatus(QueryStatus(p))
	case "status":
		if len(args) != 1 {
			return fmt.Errorf("status takes no options")
		}
		return printJSON(QueryStatus(p))
	case "logs":
		lines := &options.lines
		if err = f.Parse(args[1:]); err != nil {
			return err
		}
		if f.NArg() != 0 || *lines < 1 || *lines > 10000 {
			return fmt.Errorf("lines must be in 1..10000")
		}
		ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt)
		defer cancel()
		return showLogs(ctx, QueryStatus(p).Log, *lines, options.follow)
	case "check":
		if len(args) != 1 {
			return fmt.Errorf("check takes no options")
		}
		return printJSON(CheckConfig(p.Config))
	case "test":
		if err = f.Parse(args[1:]); err != nil {
			return err
		}
		if f.NArg() != 0 {
			return fmt.Errorf("unexpected test argument")
		}
		c, err := LoadConfig(p.Config)
		if err != nil {
			return err
		}
		return testProxy(c, options.url)
	case "trust":
		logPath := &options.log
		if err := f.Parse(args[1:]); err != nil {
			return err
		}
		if f.NArg() > 1 {
			return fmt.Errorf("usage: trust [--log FILE] [ENDPOINT_NAME]")
		}
		c, err := LoadConfig(p.Config)
		if err != nil {
			return err
		}
		var output io.Writer = os.Stdout
		var errorOutput io.Writer = os.Stderr
		if *logPath != "" {
			*logPath, err = filepath.Abs(*logPath)
			if err != nil {
				return err
			}
			_, file, err := NewLog(*logPath, false)
			if err != nil {
				return err
			}
			defer file.Close()
			output = io.MultiWriter(os.Stdout, file)
			errorOutput = io.MultiWriter(os.Stderr, file)
		}
		var failures []error
		for _, e := range c.Endpoints {
			if f.NArg() == 1 && e != f.Arg(0) {
				continue
			}
			fmt.Fprintf(output, "SSH login for %s. Verify any host fingerprint before accepting.\n", e)
			cmd := SSHCommand(c, e, true)
			cmd.Stdin = os.Stdin
			cmd.Stdout = os.Stdout
			cmd.Stderr = os.Stderr
			// Keep all interactive handles on the terminal. Native OpenSSH can
			// append diagnostics itself, without Go interposing a console pipe.
			if *logPath != "" {
				cmd.Args = append(cmd.Args[:1], append([]string{"-E", *logPath}, cmd.Args[1:]...)...)
			}
			if err = cmd.Run(); err != nil {
				failure := fmt.Errorf("SSH login for %s: %w", e, err)
				fmt.Fprintln(errorOutput, failure)
				failures = append(failures, failure)
				continue
			}
			return nil // Native SSH needs no prompt when trust/authentication already work.
		}
		if len(failures) == 0 {
			return fmt.Errorf("endpoint not found: %s", f.Arg(0))
		}
		return errors.Join(failures...)

	default:
		return fmt.Errorf("unknown command %q; use --help", args[0])
	}
}
