//go:build systemtest

package tests

import (
	"bytes"
	"context"
	"encoding/json"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"ssh_proxy/src/config"
	"ssh_proxy/src/platform"
	"ssh_proxy/tests/helpers"
)

func TestCLICommandsAndConfiguration(t *testing.T) {
	dir := testutil.IsolatedBuild(t)
	for _, command := range []string{"-v", "--version"} {
		version := strings.TrimSpace(string(testutil.MustCLI(t, dir, command)))
		if version != "ssh_proxy_cli "+strings.TrimSpace(testutil.ReadFile(filepath.Join(dir, ".version"))) {
			t.Fatal(version)
		}
	}
	if out := string(testutil.MustCLI(t, dir, "logs")); !strings.HasPrefix(out, "No log file yet:") {
		t.Fatal("missing-log notice must be text", out)
	}
	runHelp := func(args ...string) string {
		t.Helper()
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		// Help must exit successfully on stdout without loading config or doing work.
		args = append(args, "--config", filepath.Join(dir, "missing.toml"))
		cmd := exec.CommandContext(ctx, testutil.CLIPath(dir), args...)
		platform.HideChild(cmd)
		var stderr bytes.Buffer
		cmd.Stderr = &stderr
		out, err := cmd.Output()
		if err != nil || stderr.Len() != 0 || !strings.Contains(string(out), "Usage:") {
			t.Fatalf("help must succeed on stdout: %v: %v\n%s\n%s", args, err, out, &stderr)
		}
		for _, line := range strings.Split(string(out), "\n") {
			if len(strings.TrimSuffix(line, "\r")) > 80 || strings.ContainsAny(line, "\t\x1b") {
				t.Fatalf("help must fit 80 columns using plain spaces: %q", line)
			}
		}
		return string(out)
	}
	help := runHelp("--help")
	if runHelp("-h") != help {
		t.Fatal("root help aliases disagree")
	}
	if out, err := testutil.CallCLI(dir); err != nil || string(out) != help {
		t.Fatalf("no arguments must print the overview: %v %s", err, out)
	}
	for _, option := range []string{"-c, --config PATH", "-h, --help", "-v, --version", "Commands:", "Flags:", "Service commands (current user):", `Use "ssh_proxy_cli service --help" for service details.`} {
		if !strings.Contains(help, option) {
			t.Fatal("help missing", option)
		}
	}
	if strings.Contains(help, "--user") || strings.Contains(help, "_run") || strings.Contains(help, "_service") {
		t.Fatal("help must not advertise account selection or internal commands")
	}
	position := -1
	for _, name := range []string{"start", "restart", "stop", "status", "trust", "logs", "test", "check", "service install", "service uninstall", "service status", "service enable", "service disable"} {
		next := strings.Index(help, "\n  "+name)
		if next <= position {
			t.Fatal("command absent or out of order in help", name)
		}
		position = next
	}
	for _, usage := range []string{
		"start [--foreground] [--wait=SECONDS]", "restart [--foreground] [--wait=SECONDS]",
		"trust [ENDPOINT] [--check] [--log=FILE]", "logs [--follow] [--lines=N]", "test [--url=URL]",
	} {
		if !strings.Contains(help, "\n  "+usage+"\n      ") {
			t.Fatal("overview must show command flags above its description", usage)
		}
	}
	for _, tc := range []struct{ command, want string }{
		{"start", "--wait SECONDS"}, {"start", "(default 20)"}, {"restart", "--foreground"},
		{"logs", "-f, --follow"}, {"logs", "(default 60)"}, {"test", `(default "https://example.com/")`},
		{"trust", "--log FILE"}, {"trust", "--check"}, {"trust", "[ENDPOINT]"},
		{"service", "elevated terminal"}, {"service", "without sudo"},
		{"service install", "run trust"}, {"service install", "elevated terminal"}, {"service install", "without sudo"},
	} {
		page := runHelp(append(strings.Fields(tc.command), "--help")...)
		if !strings.Contains(strings.Join(strings.Fields(page), " "), tc.want) { // Ignore help wrapping.
			t.Fatalf("%s help missing %q: %s", tc.command, tc.want, page)
		}
	}
	for _, name := range []string{"start", "stop", "restart", "status", "logs", "test", "check", "trust", "service", "service install", "service uninstall", "service status", "service enable", "service disable"} {
		command := strings.Fields(name)
		if runHelp(append(command, "-h")...) != runHelp(append(command, "--help")...) {
			t.Fatalf("command help flags disagree: %s", name)
		}
	}
	if runHelp("service") != runHelp("service", "--help") {
		t.Fatal("service without a subcommand must show service help")
	}
	for _, args := range [][]string{{"help"}, {"help", "start"}, {"help", "service", "install"}, {"version"}, {"tray"}, {"login"}, {"startup", "enable"}, {"startup", "disable"}, {"startup", "status"}, {"completion"}, {"invalid"}} {
		if out, err := testutil.CallCLI(dir, args...); err == nil || !strings.Contains(string(out), "unknown command") {
			t.Fatalf("%v: %v %s", args, err, out)
		}
	}
	for _, args := range [][]string{{"start"}, {"trust"}, {"test"}} {
		if out, err := testutil.CallCLI(dir, args...); err == nil || !strings.Contains(string(out), "add your hosts") {
			t.Fatalf("%v: %v %s", args, err, out)
		}
	}
	var check config.ConfigCheck
	custom := filepath.Join(dir, "alternate config.toml")
	testutil.WriteFile(t, custom, testutil.ReadFile(filepath.Join(dir, "config.toml")))
	for _, args := range [][]string{{"-c", custom, "check"}, {"check", "-c", custom}, {"--config=" + custom, "check"}, {"-c=" + custom, "check"}} {
		out, err := testutil.CallCLI(dir, args...)
		if err != nil || json.Unmarshal(out, &check) != nil || check.Config != custom {
			t.Fatalf("short config alias: %v: %v %s", args, err, out)
		}
	}
	for _, args := range [][]string{{"--config=", "check"}, {"-c=", "check"}, {"check", "-c", ""}} {
		if out, err := testutil.CallCLI(dir, args...); err == nil || !strings.Contains(string(out), "flag needs an argument: --config") {
			t.Fatalf("empty config path must be rejected: %v: %v %s", args, err, out)
		}
	}
	if err := json.Unmarshal(testutil.MustCLI(t, dir, "check"), &check); err != nil || check.Valid || check.EditFile != filepath.Join(dir, "ssh_config") {
		t.Fatal(check, err)
	}
	if testutil.StatusCLI(t, dir).Phase != "stopped" {
		t.Fatal("fresh proxy active")
	}
	testutil.MustCLI(t, dir, "stop")
	testutil.MustCLI(t, dir, "stop")
	testutil.WriteFile(t, filepath.Join(dir, "config.toml"), "proxy = [")
	if err := json.Unmarshal(testutil.MustCLI(t, dir, "check"), &check); err != nil || check.Valid || !strings.Contains(check.Error, "toml") {
		t.Fatalf("bad TOML: %v %+v", err, check)
	}
	for _, command := range []string{"status", "check"} {
		if _, err := testutil.CallCLI(dir, command, "--json"); err == nil {
			t.Fatal("JSON is unconditional; obsolete flag accepted", command)
		}
	}
	for _, args := range [][]string{{"start"}, {"stop"}, {"restart"}, {"logs"}, {"test"}, {"trust"}, {"-v"}, {"--version"}, {"service", "status"}} {
		if out, err := testutil.CallCLI(dir, append(args, "--json")...); err == nil {
			t.Fatalf("text command must not accept --json: %v: %s", args, out)
		}
	}
	// Status and stop work even while settings are broken.
	testutil.MustCLI(t, dir, "status")
	testutil.MustCLI(t, dir, "stop")
	if out, err := testutil.CallCLI(dir, "start", "--config", filepath.Join(dir, "missing.toml")); err == nil {
		t.Fatal("missing config accepted", string(out))
	}
}
