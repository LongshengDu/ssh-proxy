// Package cli implements ssh-proxy-cli's commands, help and reports.
package cli

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"strings"
	"time"

	"github.com/spf13/cobra"
	"github.com/spf13/pflag"
	"golang.org/x/term"

	"ssh-proxy/src/bridge"
	"ssh-proxy/src/config"
	"ssh-proxy/src/control"
	"ssh-proxy/src/logs"
	"ssh-proxy/src/platform"
	"ssh-proxy/src/supervisor"
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
			_, err = io.Copy(io.Discard, io.LimitReader(resp.Body, 1024*1024))
			resp.Body.Close()
			detail, ok = resp.Status, resp.StatusCode < 400
			if err != nil {
				detail, ok = err.Error(), false
			}
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

// Only the root help shows this group; it lists each autostart command.
const autostartTitle = `Autostart commands (current user):
  Use "ssh-proxy-cli autostart --help" for login startup details.
`

// Run executes one CLI command.
func Run(args []string) error {
	if len(args) > 0 && args[0] == "_ssh" {
		return platform.RunGuardian(args[1:]) // Native ssh arguments, passed through unparsed.
	}
	run := func(do func(config.Paths) error) func(*cobra.Command, []string) error {
		return func(cmd *cobra.Command, _ []string) error {
			// pflag takes the next option as a string value, as in "login --log --help".
			var err error
			cmd.Flags().Visit(func(f *pflag.Flag) {
				if v := f.Value.String(); err == nil && f.Value.Type() == "string" && (v == "" || v == "=" || strings.HasPrefix(v, "-")) {
					err = fmt.Errorf("flag needs an argument: --%s", f.Name)
				}
			})
			if err != nil {
				return err
			}
			p, err := config.PathsFor()
			if err != nil {
				return err
			}
			return do(p)
		}
	}
	// Commands that read the configuration first create missing files from
	// support/*.in and bring config.toml up to date; status, stop, logs and
	// autostart disable/status don't.
	setup := func(do func(config.Paths) error) func(config.Paths) error {
		return func(p config.Paths) error {
			if err := config.EnsureConfig(p.Config); err != nil {
				return fmt.Errorf("prepare configuration: %w", err)
			}
			return do(p)
		}
	}
	root := &cobra.Command{Use: "ssh-proxy-cli", Short: "SSH SOCKS5 and HTTP proxy.", Version: config.Version,
		SilenceErrors: true, SilenceUsage: true, CompletionOptions: cobra.CompletionOptions{DisableDefaultCmd: true}}
	// List commands in the order added below.
	cobra.EnableCommandSorting = false
	cobra.MousetrapHelpText = ""                                           // Keep normal help when launched from Windows Explorer.
	root.SetHelpCommand(&cobra.Command{Hidden: true, GroupID: "commands"}) // No "help" command; use -h/--help.
	root.SetVersionTemplate("ssh-proxy-cli {{.Version}}\n")
	// Define boolean flags before Cobra finds the command (for example "-v extra").
	root.InitDefaultHelpFlag()
	root.InitDefaultVersionFlag()
	root.AddGroup(&cobra.Group{ID: "commands", Title: "Commands:"}, &cobra.Group{ID: "autostart", Title: autostartTitle})
	// List each command with its own flags, such as "start [--foreground] [--wait=SECONDS]",
	// and the root help lists each autostart command, such as "autostart enable".
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

Background mode requires SSH keys or ssh-agent; run login for setup. After
--wait expires, the proxy may keep retrying; check status for readiness. --wait
has no effect with --foreground.`}
		foreground := cmd.Flags().Bool("foreground", false, "Run in this terminal; Ctrl+C stops the proxy")
		wait := cmd.Flags().Int("wait", 20, "Wait up to `SECONDS` (1..3600) for background readiness")
		cmd.RunE = run(func(p config.Paths) error {
			if *wait < 1 || *wait > 3600 {
				return fmt.Errorf("--wait must be in 1..3600")
			}
			return setup(func(p config.Paths) error { return start(p, restart, *foreground, *wait) })(p)
		})
		return cmd
	}
	statusCmd := &cobra.Command{Use: "status", Short: "Show status as JSON or print a proxy URL", GroupID: "commands", Args: cobra.NoArgs,
		Long: "Show endpoint and health as JSON. URL flags print only the selected proxy URL\nwhen running, otherwise nothing. An empty result exits successfully."}
	proxyHTTP := statusCmd.Flags().Bool("proxy_http", false, "Print only the running HTTP proxy URL (http://)")
	proxySOCKS := statusCmd.Flags().Bool("proxy_socks", false, "Print only the running SOCKS5 proxy URL (socks5h://)")
	statusCmd.MarkFlagsMutuallyExclusive("proxy_http", "proxy_socks")
	statusCmd.RunE = run(func(p config.Paths) error {
		st := control.QueryStatus(p)
		if !*proxyHTTP && !*proxySOCKS {
			return printJSON(st)
		}
		if st.Phase != control.PhaseRunning {
			return nil
		}
		address, scheme := st.HTTP, "http"
		if *proxySOCKS {
			address, scheme = st.SOCKS, "socks5h"
		}
		if address == "" {
			return nil
		}
		_, err := fmt.Println(scheme + "://" + address)
		return err
	})
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
	testCmd.RunE = run(setup(func(p config.Paths) error {
		cfg, err := config.LoadConfig(p.Config)
		if err != nil {
			return err
		}
		return testProxy(cfg, *url)
	}))
	loginCmd := &cobra.Command{Use: "login [ENDPOINT]", Short: "Set up passwordless SSH login", GroupID: "commands",
		Args: cobra.MaximumNArgs(1), Long: `Set up unattended SSH login, skipping steps that are already done. Without
ENDPOINT, try SSH aliases in file order until one succeeds.

If no key exists, login creates one (OpenSSH's default ~/.ssh/id_ed25519, so no
IdentityFile line is needed). It then logs in once, where you verify the host
fingerprint and may enter your server password, and adds your public key to the
server. Finally it checks that login works without prompts. Working logins exit
0 at once. If the server refuses password login, send the printed public key to
your administrator. --check only checks and changes nothing.`}
	check := loginCmd.Flags().Bool("check", false, "Check unattended login only: no prompts or new host keys; the tray runs it before each start")
	logPath := loginCmd.Flags().String("log", "", "Also append login messages to `FILE` (used by the tray)")
	loginCmd.RunE = func(cmd *cobra.Command, args []string) error {
		return run(setup(func(p config.Paths) error { return login(p, strings.Join(args, ""), *check, *logPath) }))(cmd, args)
	}
	autostartCmd := &cobra.Command{Use: "autostart", Short: "Manage proxy startup at login", GroupID: "autostart",
		Args: cobra.NoArgs, RunE: func(cmd *cobra.Command, _ []string) error { return cmd.Help() }, Long: `Start the proxy at login for the current user, independently of the tray app.
Enable and disable change future login startup; neither starts nor stops the
current proxy. Use start, stop or restart for that. No elevation or sudo needed.
Unattended SSH login is required. Enable warns if its login check fails, but
still registers so you can enable while offline. Windows briefly opens a console
at login. Status reports registration; changes made in OS startup settings may
not be reflected. Disable before moving the installation folder.`}
	for _, sub := range [][2]string{{"enable", "Start the proxy at future logins"}, {"disable", "Remove login startup; leave the proxy running"}, {"status", "Show login registration and outdated entries"}} {
		action := sub[0]
		do := func(p config.Paths) error { return autostart(p, action) }
		if action == "enable" {
			do = setup(do) // Only enable reads the configuration.
		}
		autostartCmd.AddCommand(&cobra.Command{Use: action, Short: sub[1], Args: cobra.NoArgs, RunE: run(do)})
	}
	root.AddCommand(startCommand("start", "Start the proxy in the background", false),
		startCommand("restart", "Restart with the current configuration", true),
		&cobra.Command{Use: "stop", Short: "Stop the proxy and its SSH tunnel", GroupID: "commands", Args: cobra.NoArgs, RunE: run(stop)},
		statusCmd, loginCmd, logsCmd, testCmd,
		&cobra.Command{Use: "check", Short: "Validate configuration as JSON", GroupID: "commands", Args: cobra.NoArgs,
			Long: "Validate configuration as JSON. Inspect the valid field to determine validity.",
			RunE: run(setup(func(p config.Paths) error { return printJSON(config.CheckConfig(p.Config)) }))},
		autostartCmd,
		// Internal entry point: the background supervisor.
		&cobra.Command{Use: "_run", Hidden: true, GroupID: "commands", Args: cobra.NoArgs, RunE: run(setup(func(p config.Paths) error { return supervisor.Run(p, false) }))})
	root.SetArgs(args)
	return root.Execute()
}

func autostart(p config.Paths, action string) error {
	entry := p.AutostartEntry()
	switch action {
	case "enable":
		if _, err := config.LoadConfig(p.Config); err != nil {
			return err
		}
		if err := login(p, "", true, ""); err != nil {
			fmt.Fprintf(os.Stderr, "Warning: unattended SSH login check failed: %v\nAutostart will still be registered; run login to set it up before you log in.\n", err)
		}
		if err := entry.Enable(); err != nil {
			return err
		}
	case "disable":
		if err := entry.Disable(); err != nil {
			return err
		}
	}
	enabled, stale, err := entry.Status()
	if err != nil {
		return err
	}
	setting := "disabled"
	if enabled {
		setting = "enabled"
	}
	fmt.Printf("Proxy autostart: %s\n", setting)
	if stale {
		fmt.Println("Registration is outdated; run autostart enable to update it.")
	}
	return nil
}

func start(p config.Paths, restart, foreground bool, wait int) error {
	// A restart must not stop a working proxy because its edited config is invalid.
	if restart {
		if _, err := config.LoadConfig(p.Config); err != nil {
			p.Log = ""
			return supervisor.RecordStartFailure(p, err)
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
	if err := supervisor.StopBackground(p); err != nil {
		return err
	}
	return printStatus(control.QueryStatus(p))
}

func login(p config.Paths, endpoint string, check bool, logPath string) error {
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
	runSSH := func(cmd *exec.Cmd) error {
		if !check {
			cmd.Stdin = os.Stdin
		}
		cmd.Stdout = os.Stdout
		cmd.Stderr = os.Stderr
		// Keep all interactive handles on the terminal, where OpenSSH's own
		// messages, such as a refused password, stay visible. A silent check has
		// no reader, so native OpenSSH appends its diagnostics to the log instead.
		if logPath != "" && check {
			cmd.Args = append(cmd.Args[:1], append([]string{"-E", logPath}, cmd.Args[1:]...)...)
		}
		return cmd.Run()
	}
	var failures []error
	for _, e := range cfg.Endpoints {
		if endpoint != "" && e != endpoint {
			continue
		}
		var err error
		if check {
			fmt.Fprintf(output, "Checking unattended SSH login for %s.\n", e)
			if err = runSSH(config.SSHCommand(cfg, e, config.SSHCheck)); err != nil {
				err = fmt.Errorf("SSH login for %s: %w", e, err)
			}
		} else {
			err = setupLogin(cfg, e, runSSH, output)
		}
		if err != nil {
			fmt.Fprintln(errorOutput, err)
			failures = append(failures, err)
			continue
		}
		return nil
	}
	err = errors.Join(failures...)
	if len(failures) == 0 {
		err = fmt.Errorf("endpoint not found: %s", endpoint)
	}
	if !check && platform.OwnsConsole() && term.IsTerminal(int(os.Stdin.Fd())) {
		// The tray's setup window closes when login exits; keep the reasons above readable.
		fmt.Fprint(os.Stderr, "Press Enter to close this window.")
		bufio.NewReader(os.Stdin).ReadString('\n')
	}
	return err
}
