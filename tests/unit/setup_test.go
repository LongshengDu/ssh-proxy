package tests

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"ssh-proxy/src/config"
	"ssh-proxy/tests/helpers"
)

func TestMain(m *testing.M) {
	runSupervisorProcess()
	if testutil.RunProcess() {
		return
	}
	if err := config.CheckOpenSSH("ssh"); err != nil {
		println(err.Error())
		os.Exit(1)
	}
	os.Exit(m.Run())
}

func TestOpenSSHVersionRequirement(t *testing.T) {
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv("SSH_PROXY_TEST_PROCESS", "ssh")
	for _, tc := range []struct {
		version string
		valid   bool
	}{
		{"OpenSSH_8.7p1, LibreSSL 3.3.6", true},
		{"OpenSSH_for_Windows_9.5p2, LibreSSL 3.8.2", true},
		{"OpenSSH_10.0p1", true},
		{"OpenSSH_for_Windows_8.6p1", false},
		{"OpenSSH_8.1p1", false},
		{"not an OpenSSH client", false},
	} {
		t.Run(tc.version, func(t *testing.T) {
			t.Setenv("SSH_PROXY_TEST_VERSION", tc.version)
			err := config.CheckOpenSSH(exe)
			if (err == nil) != tc.valid {
				t.Fatalf("version check: %v", err)
			}
			if err != nil && (!strings.Contains(err.Error(), "OpenSSH 8.7 or newer is required") || !strings.Contains(err.Error(), exe)) {
				t.Fatal("missing actionable requirement/path", err)
			}
		})
	}
}

func TestConfigCheckIdentifiesEditableFile(t *testing.T) {
	for _, scenario := range []string{"empty SSH", "missing SSH", "bad SSH", "bad TOML", "missing OpenSSH", "valid"} {
		t.Run(scenario, func(t *testing.T) {
			configPath, ssh := testutil.Config(t, "Host target\n HostName fixture.invalid\n User tester\n")
			want := ssh
			switch scenario {
			case "empty SSH":
				os.WriteFile(ssh, []byte("# Add your hosts\n"), 0600)
			case "missing SSH":
				os.Remove(ssh)
			case "bad SSH":
				os.WriteFile(ssh, []byte("Host target\n Password unsupported\n"), 0600)
			case "bad TOML":
				os.WriteFile(configPath, []byte("proxy = ["), 0600)
				want = configPath
			case "missing OpenSSH":
				text := strings.ReplaceAll(string(testutil.ConfigTemplate(t)), `executable = "ssh"`, `executable = "definitely-missing-ssh-proxy-fixture"`)
				os.WriteFile(configPath, []byte(text), 0600)
				want = ""
			case "valid":
				want = ""
			}
			result := config.CheckConfig(configPath)
			if result.Valid != (scenario == "valid") || result.EditFile != want || (!result.Valid && result.Error == "") {
				t.Fatalf("wrong recovery target: %+v; want %s", result, filepath.Base(want))
			}
			// Only unfinished SSH setup is "incomplete"; real mistakes stay errors.
			if result.Incomplete != (scenario == "empty SSH" || scenario == "missing SSH") {
				t.Fatalf("wrong incomplete flag: %+v", result)
			}
		})
	}
}

func TestTrustUsesNativeSSHAndOrderedFallback(t *testing.T) {
	configPath, _ := testutil.Config(t, "Host first second\n HostName fixture.invalid\n User tester\n")
	helper, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	text := strings.ReplaceAll(string(testutil.ConfigTemplate(t)), `executable = "ssh"`, "executable = "+strconv.Quote(helper))
	if err := os.WriteFile(configPath, []byte(text), 0600); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(helper)
	if err != nil {
		t.Fatal(err)
	}
	cli := filepath.Join(filepath.Dir(configPath), filepath.Base(helper))
	if err := os.WriteFile(cli, data, 0755); err != nil {
		t.Fatal(err)
	}
	call := func(args ...string) error {
		cmd := testutil.Command(t, cli, args...)
		out, err := cmd.CombinedOutput()
		if err != nil {
			return fmt.Errorf("%w: %s", err, out)
		}
		return nil
	}
	t.Setenv("SSH_PROXY_TEST_PROCESS", "trust")
	// Setup looks for SSH keys in the home folder; never use the developer's.
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	t.Setenv("SSH_PROXY_TEST_DIAGNOSTIC", "fixture SSH refused login")
	t.Setenv("SSH_PROXY_TEST_SSH_SUCCESS", "second")
	// The tray's silent check uses the same ordered endpoints without prompting.
	if err := call("login", "--check"); err != nil {
		t.Fatal("silent check must succeed through the second endpoint", err)
	}
	if err := call("login", "--check", "first"); err == nil {
		t.Fatal("failed silent check returned success")
	}
	log := filepath.Join(t.TempDir(), "trust.txt")
	if err := call("login", "--log", log); err != nil {
		t.Fatal(err)
	}
	data, _ = os.ReadFile(log)
	if !strings.Contains(string(data), "SSH login for first") || !strings.Contains(string(data), "SSH login for second") {
		t.Fatalf("ordered fallback not logged: %s", data)
	}
	if err := call("login", "second"); err != nil {
		t.Fatal("trusted host did not exit successfully", err)
	}
	if err := call("login", "first"); err == nil {
		t.Fatal("failed SSH login returned success")
	}
	// SSH runs in the SSH config directory; a caller-relative log path must
	// still refer to the caller's directory when a silent check passes it to
	// native ssh -E.
	caller := t.TempDir()
	t.Chdir(caller)
	t.Setenv("SSH_PROXY_TEST_TERMINAL_RECORD", filepath.Join(caller, "handles.json"))
	if err := call("login", "--check", "--log", "setup/session.txt", "second"); err != nil {
		t.Fatal(err)
	}
	data, err = os.ReadFile(filepath.Join(caller, "setup/session.txt"))
	if err != nil || !strings.Contains(string(data), "fixture SSH diagnostic") {
		t.Fatal("native SSH did not append to the caller's run log", err)
	}
}
