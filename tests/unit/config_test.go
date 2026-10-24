package tests

import (
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"runtime"
	"strconv"
	"strings"
	"testing"
	"time"

	"ssh-proxy/src/config"
	"ssh-proxy/tests/helpers"
)

func TestSSHConfigPathWithoutValidation(t *testing.T) {
	home, err := os.UserHomeDir()
	if err != nil {
		t.Fatal(err)
	}
	dir := filepath.Join(t.TempDir(), "path with spaces")
	if err := os.MkdirAll(dir, 0700); err != nil {
		t.Fatal(err)
	}
	absolute := filepath.Join(t.TempDir(), "absolute_ssh_config")
	for _, tc := range []struct{ value, want string }{
		{"ssh_config", filepath.Join(dir, "ssh_config")},
		{"nested/../hosts", filepath.Join(dir, "hosts")},
		{"~/.ssh/proxy_config", filepath.Join(home, ".ssh", "proxy_config")},
		{filepath.ToSlash(absolute), absolute},
	} {
		// The rest of the file is invalid on purpose; only the SSH path matters.
		text := "[proxy]\nhttp_port = 0\n[ssh]\nexecutable = \"missing-ssh\"\nconfig_file = " + strconv.Quote(tc.value) + "\n"
		configPath := filepath.Join(dir, "config.toml")
		if err := os.WriteFile(configPath, []byte(text), 0600); err != nil {
			t.Fatal(err)
		}
		if got, err := config.SSHConfigPath(configPath); err != nil || got != tc.want {
			t.Fatalf("%s: got %q, %v; want %q", tc.value, got, err, tc.want)
		}
	}
	configPath := filepath.Join(dir, "config.toml")
	if err := os.WriteFile(configPath, []byte("[ssh]\n"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := config.SSHConfigPath(configPath); err == nil || !strings.Contains(err.Error(), "ssh.config_file") {
		t.Fatal("missing ssh.config_file accepted", err)
	}
}

func TestNativeSSHConfigIncludesAndCommand(t *testing.T) {
	path, sshPath := testutil.Config(t, "")
	includePath := filepath.Join(filepath.Dir(path), "included hosts")
	included := "Host proxy-primary\n HostName first.example.invalid\n Port 2345\n User include-user\n"
	if err := os.WriteFile(includePath, []byte(included), 0600); err != nil {
		t.Fatal(err)
	}
	sshText := "Include \"" + filepath.ToSlash(includePath) + "\"\nHost *\n User fallback\n ServerAliveInterval 27\n UserKnownHostsFile local_known_hosts\n IdentityFile relative_key\n ForkAfterAuthentication yes\n ControlMaster auto\n ControlPath shared-control\nHost proxy-primary proxy-backup\n"
	if err := os.WriteFile(sshPath, []byte(sshText), 0600); err != nil {
		t.Fatal(err)
	}
	cfg, err := config.LoadConfig(path)
	if err != nil {
		t.Fatal(err)
	}
	cmd := config.SSHCommand(cfg, cfg.Endpoints[0], config.SSHTunnel)
	if cmd.Dir != filepath.Dir(sshPath) {
		t.Fatal("wrong SSH working directory:", cmd.Dir)
	}
	if cmd.Args[1] != "-F" || cmd.Args[2] != sshPath || cmd.Args[len(cmd.Args)-1] != "proxy-primary" {
		t.Fatal(cmd.Args)
	}
	output := testutil.EffectiveSSH(t, cmd)
	for _, want := range []string{"hostname first.example.invalid\n", "port 2345\n", "user include-user\n",
		"serveraliveinterval 15\n", "userknownhostsfile local_known_hosts\n", "identityfile relative_key\n",
		"dynamicforward [127.0.0.1]:1081\n", "batchmode yes\n", "forkafterauthentication no\n", "controlmaster false\n"} {
		if !strings.Contains(output, want) {
			t.Errorf("missing effective SSH setting %q", want)
		}
	}
	second := testutil.EffectiveSSH(t, config.SSHCommand(cfg, cfg.Endpoints[1], config.SSHTunnel))
	if !strings.Contains(second, "user fallback\n") {
		t.Fatal("Host fallback ignored")
	}
}

// Every mode keeps the user's host trust, applies the TOML SSH settings and the
// managed overrides, and only the tunnel opens a SOCKS listener.
func TestManagedSSHModes(t *testing.T) {
	path, sshPath := testutil.Config(t, "Host proxy-primary\n HostName first.example.invalid\n User tester\n RemoteCommand unwanted-command\n RequestTTY force\n ForwardAgent yes\n")
	configured := strings.NewReplacer("connect_timeout_seconds = 10", "connect_timeout_seconds = 12",
		"connection_attempts = 1", "connection_attempts = 2",
		"server_alive_interval_seconds = 15", "server_alive_interval_seconds = 20",
		"server_alive_count_max = 3", "server_alive_count_max = 4").Replace(string(testutil.ConfigTemplate(t)))
	if err := os.WriteFile(path, []byte(configured), 0600); err != nil {
		t.Fatal(err)
	}
	cfg, err := config.LoadConfig(path)
	if err != nil {
		t.Fatal(err)
	}
	native := testutil.EffectiveSSH(t, exec.Command(cfg.SSH.Executable, "-F", sshPath, cfg.Endpoints[0]))
	shared := []string{"connecttimeout 12", "connectionattempts 2", "serveraliveinterval 20", "serveralivecountmax 4",
		"tcpkeepalive yes", "forwardagent no", "requesttty false"}
	for _, line := range strings.Split(native, "\n") {
		if strings.HasPrefix(line, "userknownhostsfile ") || strings.HasPrefix(line, "globalknownhostsfile ") {
			shared = append(shared, line)
		}
	}
	for _, tc := range []struct {
		mode config.SSHMode
		want []string
	}{
		{config.SSHTunnel, []string{"batchmode yes", "stricthostkeychecking true", "exitonforwardfailure yes", "dynamicforward [127.0.0.1]:1081"}},
		{config.SSHTrust, []string{"batchmode no", "stricthostkeychecking ask", "clearallforwardings yes", "stdinnull yes"}},
		{config.SSHCheck, []string{"batchmode yes", "stricthostkeychecking true", "clearallforwardings yes", "stdinnull yes"}},
	} {
		cmd := config.SSHCommand(cfg, cfg.Endpoints[0], tc.mode)
		for _, arg := range cmd.Args {
			if strings.Contains(strings.ToLower(arg), "knownhostsfile") {
				t.Fatal("application must not override native host trust", arg)
			}
		}
		if tc.mode != config.SSHTunnel {
			if cmd.Args[len(cmd.Args)-1] != "exit" {
				t.Fatal("login must run the remote exit command", cmd.Args)
			}
			cmd.Args = cmd.Args[:len(cmd.Args)-1] // Remove remote 'exit' for -G inspection.
		}
		output := testutil.EffectiveSSH(t, cmd)
		for _, want := range append(tc.want, shared...) {
			if !strings.Contains(output, want+"\n") {
				t.Errorf("mode %d: missing effective SSH setting %q", tc.mode, want)
			}
		}
		if strings.Contains(output, "remotecommand ") || (tc.mode != config.SSHTunnel && strings.Contains(output, "dynamicforward ")) {
			t.Errorf("mode %d: a user RemoteCommand or a login listener remained", tc.mode)
		}
	}
	if _, err := os.Stat(filepath.Join(filepath.Dir(path), "known_hosts")); !os.IsNotExist(err) {
		t.Fatal("application created its own trust file")
	}
}

func TestSSHAliasDiscovery(t *testing.T) {
	path, sshPath := testutil.Config(t, "# Host commented\nHost * !ignored\n User tester\nHost second first\nHost second\nHost THIRD\nHost third\nHost *.example ?other !excluded\nMatch host conditional\n User other\nHost last\n")
	cfg, err := config.LoadConfig(path)
	if err != nil {
		t.Fatal(err)
	}
	if want := []string{"second", "first", "THIRD", "last"}; !reflect.DeepEqual(cfg.Endpoints, want) {
		t.Fatalf("aliases: %v; want %v", cfg.Endpoints, want)
	}
	for _, content := range []string{"# examples only\n", "Host *\n User tester\n", "Host -oProxyCommand=bad\n"} {
		if err := os.WriteFile(sshPath, []byte(content), 0600); err != nil {
			t.Fatal(err)
		}
		if _, err := config.LoadConfig(path); err == nil {
			t.Fatalf("expected no valid candidate: %q", content)
		}
	}
	// Like Windows OpenSSH, ignore a UTF-8 BOM before the first Host line.
	if err := os.WriteFile(sshPath, []byte("\xef\xbb\xbfHost first\n"), 0600); err != nil {
		t.Fatal(err)
	}
	if aliases, err := config.SSHAliases(sshPath); err != nil || !reflect.DeepEqual(aliases, []string{"first"}) {
		t.Fatalf("aliases: %v, %v; want [first]", aliases, err)
	}
}

// A Match exec helper that outlives ssh -G keeps its stderr open; loading the
// configuration must not wait for the helper.
func TestSSHInspectionIgnoresLingeringHelper(t *testing.T) {
	path, _ := testutil.Config(t, "Host target\n")
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	text := strings.ReplaceAll(string(testutil.ConfigTemplate(t)), `executable = "ssh"`, "executable = "+strconv.Quote(exe))
	if err := os.WriteFile(path, []byte(text), 0600); err != nil {
		t.Fatal(err)
	}
	record := filepath.Join(t.TempDir(), "helper.pid")
	t.Setenv("SSH_PROXY_TEST_PROCESS", "ssh")
	t.Setenv("SSH_PROXY_TEST_LINGER", record)
	t.Cleanup(func() {
		data, _ := os.ReadFile(record)
		if pid, _ := strconv.Atoi(string(data)); pid > 0 {
			if process, err := os.FindProcess(pid); err == nil {
				process.Kill()
				if runtime.GOOS == "windows" {
					process.Wait() // Release its working directory before the folder is removed.
				}
			}
		}
	})
	began := time.Now()
	cfg, err := config.LoadConfig(path)
	// The helper runs 30 s; the limit leaves room for fixture starts under a busy suite.
	if elapsed := time.Since(began); err != nil || !reflect.DeepEqual(cfg.Endpoints, []string{"target"}) || elapsed > 20*time.Second {
		t.Fatalf("loading took %s: %v", elapsed, err)
	}
	if _, err := os.Stat(record); err != nil {
		t.Fatal("the fixture started no lingering helper:", err)
	}
}

func TestUnconfiguredInstallDoesNotResolveExampleHosts(t *testing.T) {
	path, _ := testutil.Config(t, "# Add your hosts here\n")
	template := testutil.ConfigTemplate(t)
	if err := os.WriteFile(path, template, 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := config.LoadConfig(path); err == nil || !strings.Contains(err.Error(), "add your hosts") {
		t.Fatalf("expected setup instructions for a fresh install, got %v", err)
	}
}

func TestExistingDynamicForwardAndInvalidSSHConfig(t *testing.T) {
	for _, tc := range []struct {
		name, text      string
		valid, existing bool
	}{
		{"toml-supplies-forward", "Host *\n User tester\n", true, false},
		{"ssh-supplies-forward", "Host *\n User tester\n DynamicForward 127.0.0.1:1081\n", true, true},
		{"wrong-port", "Host *\n DynamicForward 127.0.0.1:1082\n", false, false},
		{"nonloopback", "Host *\n DynamicForward 0.0.0.0:1081\n", false, false},
		{"multiple", "Host *\n DynamicForward 127.0.0.1:1081\n DynamicForward 127.0.0.1:1082\n", false, false},
		{"disabled", "Host *\n ClearAllForwardings yes\n", false, false},
		{"bad-option", "Host *\n ThisOptionDoesNotExist yes\n", false, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			path, _ := testutil.Config(t, tc.text+"\nHost proxy-primary proxy-backup\n")
			cfg, err := config.LoadConfig(path)
			if !tc.valid {
				if err == nil {
					t.Fatal("invalid SSH configuration accepted")
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			cmd := config.SSHCommand(cfg, cfg.Endpoints[0], config.SSHTunnel)
			hasD := false
			for _, arg := range cmd.Args {
				if arg == "-D" {
					hasD = true
				}
			}
			if hasD == tc.existing {
				t.Fatal("DynamicForward duplicated or missing:", cmd.Args)
			}
			effective := testutil.EffectiveSSH(t, cmd)
			if strings.Count(effective, "dynamicforward ") != 1 {
				t.Fatal("expected exactly one SOCKS listener")
			}
		})
	}
}

func TestDefaultPathsAndMissingConfig(t *testing.T) {
	path, sshPath := testutil.Config(t, "Host *\n User tester\n")
	if err := os.Remove(sshPath); err != nil {
		t.Fatal(err)
	}
	if _, err := config.LoadConfig(path); err == nil {
		t.Fatal("missing SSH config accepted")
	}
	defaults, err := config.PathsFor()
	if err != nil {
		t.Fatal(err)
	}
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	if defaults.Config != filepath.Join(filepath.Dir(exe), "config.toml") || defaults.Log != filepath.Join(filepath.Dir(exe), "logs", "logs_"+defaults.ID+".txt") {
		t.Fatal(defaults)
	}
	otherExe := filepath.Join(filepath.Dir(path), "ssh-proxy-cli")
	alternate, err := config.PathsForExecutable(otherExe)
	if err != nil {
		t.Fatal(err)
	}
	if alternate.Config != path || alternate.Logs != filepath.Join(filepath.Dir(path), "logs") || alternate.Runtime == defaults.Runtime {
		t.Fatal("separate installations must have their own config, logs and control state")
	}
	// Control/log paths remain discoverable without either configuration file.
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	absent, err := config.PathsForExecutable(otherExe)
	if err != nil || absent != alternate {
		t.Fatal(absent, err)
	}
}
