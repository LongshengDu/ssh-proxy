package tests

import (
	"errors"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"ssh_proxy/src/cli"
)

// Expected CLI output: every help topic and the error text and exit code of
// invalid invocations, recorded byte for byte. Set SSH_PROXY_UPDATE_EXPECTED=1
// to rewrite testdata/cli/expected.txt after an intended change.
var cliOutputCases = [][]string{
	// Help, with -c before or after and with options that would otherwise fail.
	{}, {"-h"}, {"--help"}, {"-c", "other.toml", "--help"}, {"--help", "--config", "other.toml"},
	{"start", "--help"}, {"restart", "-h"}, {"stop", "--help"}, {"status", "--help"},
	{"trust", "--help"}, {"logs", "--help"}, {"test", "--help"}, {"check", "--help"},
	{"start", "--wait", "0", "--help"},
	// Help wins over invalid options and arguments; unknown topics are errors.
	{"start", "--wait", "invalid", "--help"}, {"trust", "--log", "--help"}, {"logs", "--bogus", "-h"},
	{"bogus", "--help"}, {"service", "bogus", "--help"}, {"_run", "--help"}, {"stop", "extra", "--help"},
	{"service"}, {"service", "-h"}, {"service", "--help"},
	{"service", "install", "--help"}, {"service", "uninstall", "--help"}, {"service", "status", "--help"},
	{"service", "enable", "--help"}, {"service", "disable", "-h"},
	{"-v"}, {"--version"},
	// Invalid invocations rejected before any configuration, service or process access.
	{"bogus"}, {"help"}, {"version"}, {"help", "start"},
	{"-v", "extra"}, {"--config"}, {"--config="}, {"-c="},
	{"start", "--wait", "0"}, {"start", "--wait", "3601"}, {"restart", "--wait", "x"},
	{"start", "extra"}, {"start", "--bogus"}, {"--foreground", "--wait", "0"},
	{"stop", "extra"}, {"status", "--json"}, {"check", "extra"},
	{"logs", "--lines", "0"}, {"logs", "--lines", "10001"}, {"logs", "extra"},
	{"test", "extra"}, {"test", "--url"},
	{"trust", "first", "second"}, {"trust", "--log"},
	{"service", "start"}, {"service", "stop"}, {"service", "restart"},
	{"service", "bogus"}, {"service", "status", "extra"},
	{"service", "install", "--user", "other"}, {"service", "install", "extra"},
	{"_run", "extra"}, {"_service", "extra"},
	// Go flag syntax also accepts one dash and = for long options.
	{"logs", "-lines", "0"}, {"start", "-foreground", "-wait", "0"}, {"start", "--wait=0"}, {"logs", "-f", "--lines=0"},
	{"-config", "other.toml", "status"},
}

// runCLI mirrors src/cmd/ssh_proxy_cli/main.go with captured standard streams.
func runCLI(t *testing.T, args []string) (stdout, stderr string, code int) {
	t.Helper()
	dir := t.TempDir()
	outFile, err := os.Create(filepath.Join(dir, "stdout"))
	if err != nil {
		t.Fatal(err)
	}
	errFile, err := os.Create(filepath.Join(dir, "stderr"))
	if err != nil {
		t.Fatal(err)
	}
	savedOut, savedErr := os.Stdout, os.Stderr
	os.Stdout, os.Stderr = outFile, errFile
	func() {
		defer func() { os.Stdout, os.Stderr = savedOut, savedErr }()
		if err := cli.Run(args); err != nil && !errors.Is(err, flag.ErrHelp) {
			fmt.Fprintln(os.Stderr, "Error:", err)
			code = 1
		}
	}()
	outFile.Close()
	errFile.Close()
	out, _ := os.ReadFile(outFile.Name())
	errText, _ := os.ReadFile(errFile.Name())
	return string(out), string(errText), code
}

func TestCLIExpectedOutput(t *testing.T) {
	t.Setenv("SSH_PROXY_RUNTIME", t.TempDir())
	var got strings.Builder
	for _, args := range cliOutputCases {
		stdout, stderr, code := runCLI(t, args)
		fmt.Fprintf(&got, "=== ssh_proxy_cli %s\nexit %d\n--- stdout\n%s--- stderr\n%s", strings.Join(args, " "), code, stdout, stderr)
	}
	path := filepath.Join("testdata", "cli", "expected.txt")
	if os.Getenv("SSH_PROXY_UPDATE_EXPECTED") == "1" {
		if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(got.String()), 0600); err != nil {
			t.Fatal(err)
		}
	}
	want, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if got.String() != string(want) {
		gotLines, wantLines := strings.Split(got.String(), "\n"), strings.Split(string(want), "\n")
		for i := 0; i < len(gotLines) || i < len(wantLines); i++ {
			var g, w string
			if i < len(gotLines) {
				g = gotLines[i]
			}
			if i < len(wantLines) {
				w = wantLines[i]
			}
			if g != w {
				t.Fatalf("CLI output differs from %s at line %d:\n got: %q\nwant: %q", path, i+1, g, w)
			}
		}
	}
}
