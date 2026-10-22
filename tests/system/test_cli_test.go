//go:build systemtest

package tests

import (
	"bytes"
	"context"
	"encoding/json"
	"os/exec"
	"path/filepath"
	"regexp"
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
	for _, args := range [][]string{nil, {"-h"}} {
		if runHelp(args...) != help {
			t.Fatal("root help aliases disagree", args)
		}
	}
	for _, option := range []string{"-c, --config", "-h, --help", "-v, --version", "Service commands (current user):", "  Permissions:", "Windows password", "without sudo", "service --help"} {
		if !strings.Contains(help, option) {
			t.Fatal("help missing", option)
		}
	}
	if strings.Contains(help, "--user") {
		t.Fatal("help must not advertise an account selection option")
	}
	if strings.Contains(help, "Examples:") || strings.Contains(help, "\n  help ") {
		t.Fatal("overview must point to command help for details and examples")
	}
	if !strings.Contains(help, "Usage:") || !strings.Contains(help, "Commands:") || !strings.Contains(help, "Global options:") {
		t.Fatal("missing standard help sections")
	}
	for _, name := range []string{"start", "stop", "restart", "status", "logs", "test", "check", "trust", "service"} {
		if !strings.Contains(help, "  "+name+" ") {
			t.Fatal("command absent from help", name)
		}
	}
	// Catch the original defect without hard-coding a particular column number.
	row := regexp.MustCompile(`^  \S.*? {2,}(\S.*)$`)
	column, rows := -1, 0
	for _, line := range strings.Split(help, "\n") {
		if match := row.FindStringSubmatchIndex(line); match != nil {
			if column >= 0 && match[2] != column {
				t.Fatalf("description columns differ: %d vs %d: %q", column, match[2], line)
			}
			column, rows = match[2], rows+1
		}
	}
	if rows != 16 {
		t.Fatal("missing aligned command/option rows", rows)
	}
	position := -1
	for _, name := range []string{"start", "restart", "stop", "status", "trust", "logs", "test", "check"} {
		next := strings.Index(help, "\n  "+name+" ")
		if next <= position {
			t.Fatal("incorrect overview command order", name)
		}
		position = next
	}
	if !strings.Contains(help, "\n\n  trust ") {
		t.Fatal("missing separation between lifecycle and setup/diagnostic commands")
	}
	for _, tc := range []struct{ name, options, summary string }{
		{"start", "[--foreground] [--wait SECONDS]", "wait defaults to 20s"},
		{"restart", "[--foreground] [--wait SECONDS]", "wait defaults to 20s"},
		{"trust", "[--log FILE] [ENDPOINT_NAME]", "Append diagnostics"},
		{"logs", "[--follow|-f] [--lines N]", "last 60 lines by default"},
		{"test", "[--url URL]", "https://example.com/"},
	} {
		lines := strings.Split(help[strings.Index(help, "\n  "+tc.name+" ")+1:], "\n")
		if len(lines) < 2 || !strings.HasPrefix(lines[1], "    "+tc.options+"  ") || !strings.Contains(lines[1], tc.summary) {
			t.Fatal("missing indented option summary", tc.name)
		}
	}
	for _, name := range []string{"start", "stop", "restart", "status", "logs", "test", "check", "trust", "service", "service install", "service uninstall", "service status", "service enable", "service disable"} {
		command := strings.Fields(name)
		page := runHelp(append(append([]string{}, command...), "--help")...)
		if runHelp(append(command, "-h")...) != page {
			t.Fatalf("command help flags disagree: %s", name)
		}
		if strings.Contains(name, "service") && (!strings.Contains(page, "require elevation") || !strings.Contains(page, "without sudo")) {
			t.Fatal("service help missing permissions", name)
		}
	}
	for _, tc := range []struct{ name, option, value string }{
		{"start", "--wait SECONDS", "default: 20"}, {"restart", "--foreground", "default: 20"},
		{"logs", "-f, --follow", "default: 60"}, {"test", "--url URL", "default: https://example.com/"},
		{"trust", "--log FILE", "[ENDPOINT_NAME]"},
	} {
		page := runHelp(tc.name, "--help")
		if !strings.Contains(page, tc.option) || !strings.Contains(page, tc.value) {
			t.Fatalf("missing command option/default: %s: %s", tc.name, page)
		}
	}
	if runHelp("service") != runHelp("service", "--help") || runHelp("start", "--wait", "invalid", "--help") != runHelp("start", "--help") {
		t.Fatal("help must take priority over command execution and validation")
	}
	for _, args := range [][]string{{"help"}, {"help", "start"}, {"help", "service", "install"}, {"version"}, {"tray"}, {"login"}, {"startup", "enable"}, {"startup", "disable"}, {"startup", "status"}, {"invalid"}} {
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
	for _, args := range [][]string{{"-c", custom, "check"}, {"check", "-c", custom}, {"-c=" + custom, "check"}} {
		out, err := testutil.CallCLI(dir, args...)
		if err != nil || json.Unmarshal(out, &check) != nil || check.Config != custom {
			t.Fatalf("short config alias: %v: %v %s", args, err, out)
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
