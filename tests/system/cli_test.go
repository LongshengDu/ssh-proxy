//go:build systemtest

package tests

import (
	"bytes"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"ssh_proxy/src/config"
	"ssh_proxy/src/platform"
	"ssh_proxy/tests/helpers"
)

func TestCLICommandsAndConfiguration(t *testing.T) {
	t.Parallel()
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
	// A fresh install ships only the template; config.toml appears on first use.
	configPath := filepath.Join(dir, "config.toml")
	template := testutil.ReadFile(filepath.Join(dir, "support", "config.toml.in"))
	if _, err := os.Stat(configPath); !os.IsNotExist(err) {
		t.Fatal("a fresh install must not contain config.toml", err)
	}
	// Help and rejected commands are pinned by the unit snapshot; here the real
	// binary must answer them without reading or creating any configuration.
	var stderr bytes.Buffer
	help := exec.Command(testutil.CLIPath(dir), "--help")
	platform.HideChild(help)
	help.Stderr = &stderr
	if out, err := help.Output(); err != nil || stderr.Len() != 0 || !strings.Contains(string(out), "Usage:") {
		t.Fatalf("help must succeed on stdout: %v: %s %s", err, out, &stderr)
	}
	if out, err := testutil.CallCLI(dir, "help"); err == nil || !strings.Contains(string(out), "unknown command") {
		t.Fatalf("rejected command accepted: %v %s", err, out)
	}
	if _, err := os.Stat(configPath); !os.IsNotExist(err) {
		t.Fatal("help or a rejected command created config.toml", err)
	}
	for _, args := range [][]string{{"start"}, {"login"}, {"test"}} {
		testutil.WriteFile(t, configPath, template)
		if out, err := testutil.CallCLI(dir, args...); err == nil || !strings.Contains(string(out), "add your hosts") {
			t.Fatalf("%v: %v %s", args, err, out)
		}
	}
	var check config.ConfigCheck
	if err := json.Unmarshal(testutil.MustCLI(t, dir, "check"), &check); err != nil || check.Valid || check.Config != configPath || check.EditFile != filepath.Join(dir, "ssh_config") {
		t.Fatal(check, err)
	}
	if testutil.StatusCLI(t, dir).Phase != "stopped" {
		t.Fatal("fresh proxy active")
	}
	for _, flag := range []string{"--proxy_http", "--proxy_socks"} {
		if out, err := testutil.CallCLI(dir, "status", flag); err != nil || len(out) != 0 {
			t.Fatalf("stopped status %s: %v %q", flag, err, out)
		}
	}
	testutil.MustCLI(t, dir, "stop")
	testutil.MustCLI(t, dir, "stop")
	testutil.WriteFile(t, filepath.Join(dir, "config.toml"), "proxy = [")
	if err := json.Unmarshal(testutil.MustCLI(t, dir, "check"), &check); err != nil || check.Valid || !strings.Contains(check.Error, "toml") {
		t.Fatalf("bad TOML: %v %+v", err, check)
	}
	// Status and stop work even while settings are broken.
	testutil.MustCLI(t, dir, "status")
	testutil.MustCLI(t, dir, "stop")
	if err := os.Remove(configPath); err != nil {
		t.Fatal(err)
	}
	if out, err := testutil.CallCLI(dir, "start"); err == nil {
		t.Fatal("missing config accepted", string(out))
	}
}
