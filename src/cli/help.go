package cli

import (
	"flag"
	"fmt"
	"io"
	"os"
	"strings"
	"text/tabwriter"
)

type cliOptions struct {
	foreground, follow, batch bool
	wait, lines               int
	url, log                  string
}

// The same flag definitions drive parsing, option names and displayed defaults.
func commandFlags(name string) (*flag.FlagSet, *cliOptions) {
	f := flag.NewFlagSet(name, flag.ContinueOnError)
	f.SetOutput(os.Stderr)
	f.Usage = func() { _ = writeHelp(f.Output(), name, f) }
	o := &cliOptions{}
	switch name {
	case "start", "restart":
		f.BoolVar(&o.foreground, "foreground", false, "Run in this terminal; Ctrl+C stops the proxy")
		f.IntVar(&o.wait, "wait", 20, "Wait `SECONDS` for background readiness")
	case "logs":
		f.BoolVar(&o.follow, "follow", false, "Follow new log lines")
		f.BoolVar(&o.follow, "f", false, "Follow new log lines")
		f.IntVar(&o.lines, "lines", 60, "Show the last `N` lines")
	case "test":
		f.StringVar(&o.url, "url", "https://example.com/", "Fetch `URL`")
	case "trust":
		f.BoolVar(&o.batch, "batch", false, "Check unattended login only: no prompts or new host keys")
		f.StringVar(&o.log, "log", "", "Append SSH diagnostics to `FILE` (used by the tray)")
	}
	return f, o
}

type commandDoc struct{ name, summary, args, notes, examples string }

const startNotes = `Background mode requires SSH keys or ssh-agent. Run trust for setup.
After --wait expires, the proxy may keep retrying; check status for readiness.
--wait accepts 1..3600 seconds and has no effect with --foreground.`

var commandDocs = []commandDoc{
	{"start", "Start the proxy in the background", "[options]", startNotes, "start\nstart --foreground"},
	{"restart", "Restart with the current configuration", "[options]", startNotes, "restart\nrestart --wait 30"},
	{"stop", "Stop the proxy and its SSH tunnel", "[options]", "", "stop"},
	{"status", "Show endpoint and health as JSON", "[options]", "", "status"},
	{"trust", "Set up SSH login and host-key trust", "[options] [ENDPOINT_NAME]", "Without an endpoint, try SSH aliases in file order until one succeeds.\nVerify host fingerprints before accepting. Already working logins exit 0.\n--batch fails instead of prompting; the tray runs it before each start.", "trust\ntrust my-proxy"},
	{"logs", "Show or follow proxy logs", "[options]", "--lines accepts 1..10000. Output is plain text.", "logs\nlogs --follow"},
	{"test", "Test HTTP and SOCKS5 forwarding", "[options]", "The proxy must be running. A failed check returns a nonzero exit code.", "test\ntest --url https://example.com/"},
	{"check", "Validate configuration as JSON", "[options]", "Inspect the valid field in the JSON report to determine validity.", "check\n-c ./config.toml check"},
	{"service", "Manage the proxy service for the current user", "<command> [options]", "", "service install\nstart\nservice status"},
	{"service install", "Register and enable automatic startup", "[options]", "Run trust and confirm unattended login works, then stop before installing.", "service install\nstart"},
	{"service uninstall", "Stop and remove the service", "[options]", "", "service uninstall"},
	{"service status", "Show service state and automatic startup", "[options]", "", "service status"},
	{"service enable", "Enable automatic startup", "[options]", "", "service enable"},
	{"service disable", "Disable automatic startup", "[options]", "", "service disable"},
}

const serviceNotes = `Service behavior:
  Registration uses the current user on every OS.
  Install enables automatic startup without starting the proxy.
  Enable/disable change automatic startup; use start, stop or restart to run it.
  On macOS, re-enable a disabled service before starting it manually.

Permissions:
  Windows	Install, uninstall, enable and disable require elevation.
  	Install prompts for the current Windows account password.
  macOS/Linux	Run as the current user, without sudo.
`

func writeHelp(out io.Writer, name string, flags *flag.FlagSet) error {
	doc := commandDoc{summary: "ssh_proxy_cli - SSH SOCKS5 and HTTP proxy", args: "[options] <command> [command options]"}
	if name != "" {
		doc = commandDoc{}
		for _, candidate := range commandDocs {
			if candidate.name == name {
				doc = candidate
				break
			}
		}
		if doc.name == "" {
			return fmt.Errorf("unknown help topic %q; use --help", name)
		}
	}
	// Every table uses the same minimum column width; blank lines reset groups.
	w := tabwriter.NewWriter(out, 22, 0, 2, ' ', 0)
	usage := strings.TrimSpace("ssh_proxy_cli " + name)
	fmt.Fprintf(w, "%s\n\nUsage:\n  %s %s\n", doc.summary, usage, doc.args)
	if name == "" || name == "service" {
		fmt.Fprintln(w, "\nCommands:")
		for _, item := range commandDocs {
			if name == "" && !strings.Contains(item.name, " ") && item.name != "service" {
				if item.name == "trust" {
					fmt.Fprintln(w)
				}
				fmt.Fprintf(w, "  %s\t%s\n", item.name, item.summary)
				f, _ := commandFlags(item.name)
				var options, summary string
				switch item.name {
				case "start", "restart":
					options = "[--foreground] [--wait SECONDS]"
					summary = "Foreground; wait defaults to " + f.Lookup("wait").DefValue + "s"
				case "trust":
					options, summary = "[--log FILE] [ENDPOINT_NAME]", "Append diagnostics; select an endpoint"
				case "logs":
					options = "[--follow|-f] [--lines N]"
					summary = "Follow; last " + f.Lookup("lines").DefValue + " lines by default"
				case "test":
					options, summary = "[--url URL]", "Default: "+f.Lookup("url").DefValue
				}
				if options != "" {
					fmt.Fprintf(w, "    %-31s  %s\n", options, summary)
				}
			} else if name == "service" && strings.HasPrefix(item.name, "service ") {
				fmt.Fprintf(w, "  %s\t%s\n", strings.TrimPrefix(item.name, "service "), item.summary)
			}
		}
		if name == "" {
			fmt.Fprintln(w, "\n  Use \"ssh_proxy_cli <command> --help\" for details and examples.")
			fmt.Fprintln(w, "\nService commands (current user):")
			for _, item := range commandDocs {
				if strings.HasPrefix(item.name, "service ") {
					fmt.Fprintf(w, "  %s\t%s\n", item.name, item.summary)
				}
			}
			fmt.Fprint(w, `
  Permissions:
    Windows      Run service commands in an elevated terminal.
                 Install prompts for your Windows password.
    macOS/Linux  Run as the current user, without sudo.

  Use "ssh_proxy_cli service --help" for service details.
`)
		}
	}
	if flags != nil {
		heading := false
		flags.VisitAll(func(f *flag.Flag) {
			if f.Name == "f" { // Display the follow alias on one row.
				return
			}
			if !heading {
				fmt.Fprintln(w, "\nOptions:")
				heading = true
			}
			label := "--" + f.Name
			if f.Name == "follow" {
				label = "-f, --follow"
			}
			value, description := flag.UnquoteUsage(f)
			if value != "" {
				label += " " + value
			}
			if f.DefValue != "" && f.DefValue != "false" {
				description += " (default: " + f.DefValue + ")"
			}
			fmt.Fprintf(w, "  %s\t%s\n", label, description)
		})
	}
	fmt.Fprint(w, "\nGlobal options:\n  -c, --config PATH\tRead configuration from PATH\n  \t(default: config.toml beside the executable)\n  -h, --help\tShow help\n")
	if name == "" {
		fmt.Fprintln(w, "  -v, --version\tPrint the version")
	}
	if doc.notes != "" {
		fmt.Fprintln(w, "\nNotes:\n  "+strings.ReplaceAll(doc.notes, "\n", "\n  "))
	}
	if strings.HasPrefix(name, "service") {
		fmt.Fprint(w, "\n", serviceNotes)
	}
	if doc.examples != "" {
		fmt.Fprintln(w, "\nExamples:")
		for _, example := range strings.Split(doc.examples, "\n") {
			fmt.Fprintln(w, "  ssh_proxy_cli "+example)
		}
	}
	return w.Flush()
}
