package tests

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"ssh_proxy/src/proxy"
	"ssh_proxy/tests/helpers"
)

func TestMain(m *testing.M) {
	if testutil.RunProcess() {
		return
	}
	os.Exit(m.Run())
}

func TestConfigCheckIdentifiesEditableFile(t *testing.T) {
	for _, scenario := range []string{"empty SSH", "missing SSH", "bad SSH", "bad TOML", "missing OpenSSH", "valid"} {
		t.Run(scenario, func(t *testing.T) {
			config, ssh := testutil.Config(t, "Host target\n HostName fixture.invalid\n User tester\n")
			want := ssh
			switch scenario {
			case "empty SSH":
				os.WriteFile(ssh, []byte("# Add your hosts\n"), 0600)
			case "missing SSH":
				os.Remove(ssh)
			case "bad SSH":
				os.WriteFile(ssh, []byte("Host target\n Password unsupported\n"), 0600)
			case "bad TOML":
				os.WriteFile(config, []byte("proxy = ["), 0600)
				want = config
			case "missing OpenSSH":
				text := strings.ReplaceAll(string(testutil.ConfigTemplate(t)), `executable = "ssh"`, `executable = "definitely-missing-ssh-proxy-fixture"`)
				os.WriteFile(config, []byte(text), 0600)
				want = ""
			case "valid":
				want = ""
			}
			result := proxy.CheckConfig(config)
			if result.Valid != (scenario == "valid") || result.EditFile != want || (!result.Valid && result.Error == "") {
				t.Fatalf("wrong recovery target: %+v; want %s", result, filepath.Base(want))
			}
		})
	}
}

func TestTrustUsesNativeSSHAndOrderedFallback(t *testing.T) {
	config, _ := testutil.Config(t, "Host first second\n HostName fixture.invalid\n User tester\n")
	helper, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	text := strings.ReplaceAll(string(testutil.ConfigTemplate(t)), `executable = "ssh"`, "executable = "+strconv.Quote(helper))
	if err := os.WriteFile(config, []byte(text), 0600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("SSH_PROXY_TEST_PROCESS", "ssh")
	t.Setenv("SSH_PROXY_TEST_DIAGNOSTIC", "fixture SSH refused login")
	t.Setenv("SSH_PROXY_TEST_SSH_SUCCESS", "second")
	log := filepath.Join(t.TempDir(), "trust.txt")
	if err := proxy.Run([]string{"trust", "--log", log, "--config", config}); err != nil {
		t.Fatal(err)
	}
	data, _ := os.ReadFile(log)
	if !strings.Contains(string(data), "SSH login for first") || !strings.Contains(string(data), "SSH login for second") {
		t.Fatalf("ordered fallback not logged: %s", data)
	}
	if err := proxy.Run([]string{"trust", "second", "--config", config}); err != nil {
		t.Fatal("trusted host did not exit successfully", err)
	}
	if err := proxy.Run([]string{"trust", "first", "--config", config}); err == nil {
		t.Fatal("failed SSH login returned success")
	}
	// SSH runs in the SSH config directory; a caller-relative log path must
	// still refer to the caller's directory when passed to native ssh -E.
	caller := t.TempDir()
	t.Chdir(caller)
	t.Setenv("SSH_PROXY_TEST_TERMINAL_RECORD", filepath.Join(caller, "handles.json"))
	if err := proxy.Run([]string{"trust", "--log", "setup/session.txt", "second", "--config", config}); err != nil {
		t.Fatal(err)
	}
	data, err = os.ReadFile(filepath.Join(caller, "setup/session.txt"))
	if err != nil || !strings.Contains(string(data), "fixture SSH diagnostic") {
		t.Fatal("native SSH did not append to the caller's run log", err)
	}
}
