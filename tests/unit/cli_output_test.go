package tests

import (
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
	// Record each distinct help output once; equivalent invocations are checked below.
	{},
	{"start", "--help"}, {"restart", "-h"}, {"stop", "--help"}, {"status", "--help"},
	{"login", "--help"}, {"logs", "--help"}, {"test", "--help"}, {"check", "--help"},
	// Record help combined with invalid options/arguments, using Cobra's parsing rules.
	{"start", "--wait", "invalid", "--help"}, {"login", "--log", "--help"}, {"logs", "--bogus", "-h"},
	{"bogus", "--help"}, {"_run", "--help"},
	{"autostart"},
	{"autostart", "status", "--help"},
	{"autostart", "enable", "--help"}, {"autostart", "disable", "-h"},
	{"-v"},
	// Invalid invocations rejected before any configuration, service or process access.
	{"bogus"}, {"help"}, {"version"}, {"help", "start"},
	{"-v", "extra"}, {"--config"},
	{"check", "-c", "other.toml"},
	{"start", "--wait", "0"}, {"start", "--wait", "3601"}, {"restart", "--wait", "x"},
	{"start", "extra"}, {"start", "--bogus"}, {"--foreground", "--wait", "0"},
	{"stop", "extra"}, {"status", "--json"}, {"check", "extra"},
	{"status", "--proxy_http", "--proxy_socks"},
	{"logs", "--lines", "0"}, {"logs", "--lines", "10001"}, {"logs", "extra"},
	{"test", "extra"}, {"test", "--url"},
	{"login", "first", "second"}, {"login", "--log"}, {"trust"},
	{"service", "start"}, {"service", "stop"}, {"service", "restart"},
	{"service", "bogus"}, {"autostart", "status", "extra"}, {"autostart", "bogus"},
	{"service", "install", "--user", "other"}, {"service", "install", "extra"},
	{"_run", "extra"}, {"_service", "extra"},
	// Long options take two dashes, with a space or = before the value; one dash is rejected.
	{"logs", "-lines", "0"}, {"start", "-foreground", "-wait", "0"}, {"start", "--wait=0"}, {"logs", "-f", "--lines=0"},
	{"-config", "other.toml", "status"},
}

func TestCLIEquivalentOutput(t *testing.T) {
	t.Setenv("SSH_PROXY_RUNTIME", t.TempDir())
	for _, group := range [][][]string{
		{{}, {"-h"}, {"--help"}},
		{{"autostart"}, {"autostart", "-h"}, {"autostart", "--help"}, {"autostart", "bogus", "--help"}},
		{{"start", "--help"}, {"start", "--wait", "0", "--help"}},
		{{"stop", "--help"}, {"stop", "extra", "--help"}},
		{{"-v"}, {"--version"}},
	} {
		out, errText, code := runCLI(t, group[0])
		for _, args := range group[1:] {
			t.Run(strings.Join(args, " "), func(t *testing.T) {
				gotOut, gotErr, gotCode := runCLI(t, args)
				if gotOut != out || gotErr != errText || gotCode != code {
					t.Fatalf("output differs from %v: exit=%d stdout=%q stderr=%q", group[0], gotCode, gotOut, gotErr)
				}
			})
		}
	}
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
		if err := cli.Run(args); err != nil {
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
