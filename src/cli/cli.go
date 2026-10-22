// Package cli implements ssh_proxy_cli's commands, help and reports.
package cli

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
	"strings"
	"time"

	"github.com/spf13/cobra"
	"github.com/spf13/pflag"

	"ssh_proxy/src/bridge"
	"ssh_proxy/src/config"
	"ssh_proxy/src/control"
	"ssh_proxy/src/logs"
	"ssh_proxy/src/platform"
	"ssh_proxy/src/service"
	"ssh_proxy/src/supervisor"
)

func printJSON(value any) error {
	encoder := json.NewEncoder(os.Stdout)
	encoder.SetIndent("", "  ")
	return encoder.Encode(value)
}
func printStatus(st control.Status) error {
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

func testProxy(c *config.Config, target string) error {
	u, err := url.Parse(target)
	if err != nil || u.Host == "" || (u.Scheme != "http" && u.Scheme != "https") {
		return fmt.Errorf("test URL must use http:// or https://")
	}
	dial, err := bridge.SOCKSDialer(c.SocksAddr(), 10*time.Second)
	if err != nil {
		return err
	}
	proxyURL := &url.URL{Scheme: "http", Host: c.HTTPAddr()}
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

// Only the root help shows this group; it lists each service command.
const serviceTitle = `Service commands (current user):
  Use "ssh_proxy_cli service --help" for service details.
`

// Run executes one CLI command.
func Run(args []string) error {
	if len(args) > 0 && args[0] == "_ssh" {
		return platform.RunGuardian(args[1:]) // Native ssh arguments, passed through unparsed.
	}
	var configPath string
	run := func(do func(config.Paths) error) func(*cobra.Command, []string) error {
		return func(cmd *cobra.Command, _ []string) error {
			// pflag accepts "--config=", reads "-c=" as the path "=", and takes the next
			// argument as a value even when it is an option, as in "trust --log --help".
			var err error
			cmd.Flags().Visit(func(f *pflag.Flag) {
				if v := f.Value.String(); err == nil && f.Value.Type() == "string" && (v == "" || v == "=" || strings.HasPrefix(v, "-")) {
					err = fmt.Errorf("flag needs an argument: --%s", f.Name)
				}
			})
			if err != nil {
				return err
			}
			p, err := config.PathsFor(configPath)
			if err != nil {
				return err
			}
			return do(p)
		}
	}
	root := &cobra.Command{Use: "ssh_proxy_cli", Short: "SSH SOCKS5 and HTTP proxy.", Version: config.Version,
		SilenceErrors: true, SilenceUsage: true, CompletionOptions: cobra.CompletionOptions{DisableDefaultCmd: true}}
	// List commands in the order added below.
	cobra.EnableCommandSorting = false
	cobra.MousetrapHelpText = ""                                           // Keep normal help when launched from Windows Explorer.
	root.SetHelpCommand(&cobra.Command{Hidden: true, GroupID: "commands"}) // No "help" command; use -h/--help.
	root.SetVersionTemplate("ssh_proxy_cli {{.Version}}\n")
	// Define -h and -v before cobra finds the command; otherwise it reads "--help --config PATH"
	// as --help taking the value --config, and "-v extra" as -v taking the value extra.
	root.InitDefaultHelpFlag()
	root.InitDefaultVersionFlag()
	root.PersistentFlags().StringVarP(&configPath, "config", "c", "", "Read configuration from `PATH` (default: config.toml beside the CLI)")
	root.AddGroup(&cobra.Group{ID: "commands", Title: "Commands:"}, &cobra.Group{ID: "service", Title: serviceTitle})
	// List each command with its own flags, such as "start [--foreground] [--wait=SECONDS]",
	// and the root help lists each service command, such as "service install".
	var commandLine func(c *cobra.Command) string
	commandLine = func(c *cobra.Command) string {
		if c.HasAvailableSubCommands() {
			lines := []string{}
			for _, sub := range c.Commands() {
				lines = append(lines, c.Name()+" "+commandLine(sub))
			}
			return strings.Join(lines, "\n  ")
		}
		line := c.Use
		c.LocalNonPersistentFlags().VisitAll(func(f *pflag.Flag) {
			if value, _ := pflag.UnquoteUsage(f); value != "" {
				line += " [--" + f.Name + "=" + value + "]"
			} else if f.Name != "help" {
				line += " [--" + f.Name + "]"
			}
		})
		return line + "\n      " + c.Short
	}
	cobra.AddTemplateFunc("commandLine", commandLine)
	// Use commandLine and keep help within 80 columns.
	root.SetUsageTemplate(strings.NewReplacer("{{rpad .Name .NamePadding }} {{.Short}}", "{{commandLine .}}",
		"FlagUsages", "FlagUsagesWrapped 80", "for more information about a command", "for command details").Replace(root.UsageTemplate()))

	startCommand := func(name, short string, restart bool) *cobra.Command {
		cmd := &cobra.Command{Use: name, Short: short, GroupID: "commands", Args: cobra.NoArgs, Long: short + `

Background mode requires SSH keys or ssh-agent; run trust for setup. After
--wait expires, the proxy may keep retrying; check status for readiness. --wait
has no effect with --foreground.`}
		foreground := cmd.Flags().Bool("foreground", false, "Run in this terminal; Ctrl+C stops the proxy")
		wait := cmd.Flags().Int("wait", 20, "Wait up to `SECONDS` (1..3600) for background readiness")
		cmd.RunE = run(func(p config.Paths) error {
			if *wait < 1 || *wait > 3600 {
				return fmt.Errorf("--wait must be in 1..3600")
			}
			return start(p, restart, *foreground, *wait)
		})
		return cmd
	}
	logsCmd := &cobra.Command{Use: "logs", Short: "Show or follow proxy logs", GroupID: "commands", Args: cobra.NoArgs}
	follow := logsCmd.Flags().BoolP("follow", "f", false, "Follow new log lines")
	lines := logsCmd.Flags().Int("lines", 60, "Show the last `N` (1..10000) lines")
	logsCmd.RunE = run(func(p config.Paths) error {
		if *lines < 1 || *lines > 10000 {
			return fmt.Errorf("--lines must be in 1..10000")
		}
		ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt)
		defer cancel()
		return logs.Show(ctx, control.QueryStatus(p).Log, *lines, *follow)
	})
	testCmd := &cobra.Command{Use: "test", Short: "Test HTTP and SOCKS5 forwarding", GroupID: "commands", Args: cobra.NoArgs}
	url := testCmd.Flags().String("url", "https://example.com/", "Fetch `URL` through both proxies")
	testCmd.RunE = run(func(p config.Paths) error {
		cfg, err := config.LoadConfig(p.Config)
		if err != nil {
			return err
		}
		return testProxy(cfg, *url)
	})
	trustCmd := &cobra.Command{Use: "trust [ENDPOINT]", Short: "Set up SSH login and host-key trust", GroupID: "commands",
		Args: cobra.MaximumNArgs(1), Long: `Set up SSH login and host-key trust. Without ENDPOINT, try SSH aliases in file
order until one succeeds. Verify host fingerprints before accepting. Already
working logins exit 0.`}
	check := trustCmd.Flags().Bool("check", false, "Check unattended login only: no prompts or new host keys; the tray runs it before each start")
	logPath := trustCmd.Flags().String("log", "", "Append SSH diagnostics to `FILE` (used by the tray)")
	trustCmd.RunE = func(cmd *cobra.Command, args []string) error {
		return run(func(p config.Paths) error { return trust(p, strings.Join(args, ""), *check, *logPath) })(cmd, args)
	}
	// Runnable, so that cobra rejects arguments such as "service start" instead of printing help.
	serviceCmd := &cobra.Command{Use: "service", Short: "Manage the proxy service for the current user", GroupID: "service",
		Args: cobra.NoArgs, RunE: func(cmd *cobra.Command, _ []string) error { return cmd.Help() }, Long: `Manage the proxy service for the current user. Registration uses the current
user on every OS. Install enables automatic startup without starting the proxy;
run trust and confirm unattended login works, then stop the proxy before
installing. Enable/disable change automatic startup; use start, stop or restart
to run it. On macOS, re-enable a disabled service before starting it manually.
On Windows, install, uninstall, enable and disable need an elevated terminal,
and install prompts for your Windows password; on macOS and Linux run as the
current user, without sudo.`}
	for _, sub := range [][2]string{{"install", "Register and enable automatic startup"}, {"uninstall", "Stop and remove the service"},
		{"status", "Show service state and automatic startup"}, {"enable", "Enable automatic startup"}, {"disable", "Disable automatic startup"}} {
		action := sub[0]
		cmd := &cobra.Command{Use: action, Short: sub[1], Args: cobra.NoArgs,
			RunE: run(func(p config.Paths) error { return service.Manage(p, action) })}
		if action == "install" {
			cmd.Long = `Register and enable automatic startup; install does not start the proxy.

Before installing, run trust and confirm unattended login works, then stop the
proxy. On Windows, run install in an elevated terminal; it prompts for your
Windows password. On macOS and Linux, run it as the current user, without sudo.
See "ssh_proxy_cli service --help" for details.`
		}
		serviceCmd.AddCommand(cmd)
	}
	root.AddCommand(startCommand("start", "Start the proxy in the background", false),
		startCommand("restart", "Restart with the current configuration", true),
		&cobra.Command{Use: "stop", Short: "Stop the proxy and its SSH tunnel", GroupID: "commands", Args: cobra.NoArgs, RunE: run(stop)},
		&cobra.Command{Use: "status", Short: "Show endpoint and health as JSON", GroupID: "commands", Args: cobra.NoArgs,
			RunE: run(func(p config.Paths) error { return printJSON(control.QueryStatus(p)) })},
		trustCmd, logsCmd, testCmd,
		&cobra.Command{Use: "check", Short: "Validate configuration as JSON", GroupID: "commands", Args: cobra.NoArgs,
			Long: "Validate configuration as JSON. Inspect the valid field to determine validity.",
			RunE: run(func(p config.Paths) error { return printJSON(config.CheckConfig(p.Config)) })},
		serviceCmd,
		// Internal entry points: the background supervisor and the OS service.
		&cobra.Command{Use: "_run", Hidden: true, GroupID: "commands", Args: cobra.NoArgs, RunE: run(func(p config.Paths) error { return supervisor.Run(p, false) })},
		&cobra.Command{Use: "_service", Hidden: true, GroupID: "commands", Args: cobra.NoArgs, RunE: run(service.Run)})
	root.SetArgs(args)
	return root.Execute()
}

func start(p config.Paths, restart, foreground bool, wait int) error {
	action := "start"
	if restart {
		action = "restart"
	}
	// Validate once before touching a service or a running instance; _run validates again.
	if !foreground || restart {
		if _, err := config.LoadConfig(p.Config); err != nil {
			return err
		}
	}
	if !foreground {
		handled, err := service.Action(p, action, time.Duration(wait)*time.Second)
		if err != nil {
			return err
		}
		if handled {
			return printStatus(control.QueryStatus(p))
		}
	}
	if restart {
		if foreground {
			if _, err := service.Action(p, "stop", 0); err != nil {
				return err
			}
		}
		if err := supervisor.StopBackground(p); err != nil {
			return err
		}
	}
	var err error
	if foreground {
		err = supervisor.Run(p, true)
	} else {
		err = supervisor.StartBackground(p, time.Duration(wait)*time.Second)
	}
	if err != nil {
		return err
	}
	return printStatus(control.QueryStatus(p))
}

func stop(p config.Paths) error {
	handled, err := service.Action(p, "stop", 0)
	if err != nil {
		return err
	}
	if !handled {
		if err := supervisor.StopBackground(p); err != nil {
			return err
		}
	}
	return printStatus(control.QueryStatus(p))
}

func trust(p config.Paths, endpoint string, check bool, logPath string) error {
	cfg, err := config.LoadConfig(p.Config)
	if err != nil {
		return err
	}
	var output io.Writer = os.Stdout
	var errorOutput io.Writer = os.Stderr
	if logPath != "" {
		logPath, err = filepath.Abs(logPath)
		if err != nil {
			return err
		}
		_, file, err := logs.NewLog(logPath, false)
		if err != nil {
			return err
		}
		defer file.Close()
		file.SetLimit(0) // The process that owns this log applies its size limit.
		output = io.MultiWriter(os.Stdout, file)
		errorOutput = io.MultiWriter(os.Stderr, file)
	}
	var failures []error
	for _, e := range cfg.Endpoints {
		if endpoint != "" && e != endpoint {
			continue
		}
		mode := config.SSHTrust
		if check {
			mode = config.SSHCheck
			fmt.Fprintf(output, "Checking unattended SSH login for %s.\n", e)
		} else {
			fmt.Fprintf(output, "SSH login for %s. Verify any host fingerprint before accepting.\n", e)
		}
		cmd := config.SSHCommand(cfg, e, mode)
		if !check {
			cmd.Stdin = os.Stdin
		}
		cmd.Stdout = os.Stdout
		cmd.Stderr = os.Stderr
		// Keep all interactive handles on the terminal. Native OpenSSH can
		// append diagnostics itself, without Go interposing a console pipe.
		if logPath != "" {
			cmd.Args = append(cmd.Args[:1], append([]string{"-E", logPath}, cmd.Args[1:]...)...)
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
		return fmt.Errorf("endpoint not found: %s", endpoint)
	}
	return errors.Join(failures...)
}
