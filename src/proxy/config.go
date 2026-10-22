package proxy

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"github.com/BurntSushi/toml"
	sshconfig "github.com/trzsz/ssh_config"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"strconv"
	"strings"
	"time"
)

// Config holds supervisor settings; OpenSSH owns connection settings.
type Config struct {
	Endpoints []string `toml:"-"` // Discovered from explicit Host aliases in ssh_config.
	Proxy     struct {
		Bind      string `toml:"bind"`
		SocksPort int    `toml:"socks_port"`
		HTTPPort  int    `toml:"http_port"`
	} `toml:"proxy"`
	SSH struct {
		Executable          string `toml:"executable"`
		ConfigFile          string `toml:"config_file"`
		ConnectTimeout      int    `toml:"connect_timeout_seconds"`
		ConnectionAttempts  int    `toml:"connection_attempts"`
		ServerAliveInterval int    `toml:"server_alive_interval_seconds"`
		ServerAliveCountMax int    `toml:"server_alive_count_max"`
		ReadyTimeout        int    `toml:"ready_timeout_seconds"`
		RetryDelay          int    `toml:"retry_delay_seconds"`
	} `toml:"ssh"`
	Health struct {
		Targets                []string `toml:"targets"`
		FailoverOnProbeFailure bool     `toml:"failover_on_probe_failure"`
		Interval               int      `toml:"interval_seconds"`
		Timeout                int      `toml:"timeout_seconds"`
		Failures               int      `toml:"failures_before_failover"`
	} `toml:"health"`
	Logging struct {
		MaxLogFiles int `toml:"max_log_files"`
		MaxSizeMB   int `toml:"max_size_mb"`
	} `toml:"logging"`
	Path     string `toml:"-"`
	resolved map[string]sshSettings
}
type sshSettings struct {
	destination       string
	hasDynamicForward bool
}
type Paths struct{ Config, Runtime, State, Logs, Log, ID string }

// ConfigCheck identifies the file to edit without making the tray parse errors.
type ConfigCheck struct {
	Valid     bool     `json:"valid"`
	EditFile  string   `json:"edit_file,omitempty"`
	Error     string   `json:"error,omitempty"`
	Config    string   `json:"config"`
	SSH       string   `json:"ssh_executable,omitempty"`
	SSHConfig string   `json:"ssh_config,omitempty"`
	Endpoints []string `json:"endpoints,omitempty"`
}

type configFileError struct {
	path string
	err  error
}

func (e *configFileError) Error() string { return e.err.Error() }
func (e *configFileError) Unwrap() error { return e.err }

func CheckConfig(path string) ConfigCheck {
	c, err := LoadConfig(path)
	result := ConfigCheck{Valid: err == nil, Config: path}
	if err != nil {
		result.Error = err.Error()
		var file *configFileError
		if errors.As(err, &file) {
			result.EditFile = file.path
		}
	} else {
		result.Config, result.SSH, result.SSHConfig, result.Endpoints = c.Path, c.SSH.Executable, c.SSH.ConfigFile, c.Endpoints
	}
	return result
}

// PathsFor finds the default beside the executable, independent of working directory.
func PathsFor(configPath string) (Paths, error) {
	exe, err := os.Executable()
	if err != nil {
		return Paths{}, err
	}
	return PathsForExecutable(configPath, exe)
}

// PathsForExecutable lets the separate tray find the backend's portable data.
func PathsForExecutable(configPath, exe string) (Paths, error) {
	if configPath == "" {
		configPath = filepath.Join(filepath.Dir(exe), "config.toml")
	}
	abs, err := filepath.Abs(configPath)
	if err != nil {
		return Paths{}, err
	}
	identity := filepath.Clean(abs)
	if runtime.GOOS == "windows" {
		identity = strings.ToLower(identity)
	}
	hash := sha256.Sum256([]byte(identity))
	id := hex.EncodeToString(hash[:8])
	base := os.Getenv("LOCALAPPDATA")
	if base == "" {
		base, err = os.UserCacheDir()
		if err != nil {
			return Paths{}, err
		}
	}
	runtimeDir := filepath.Join(base, "ssh_proxy", id)
	if override := os.Getenv("SSH_PROXY_RUNTIME"); override != "" {
		runtimeDir = override
	}
	// Discover the latest proxy run even after its control state has been removed.
	logs := filepath.Join(filepath.Dir(exe), "logs")
	return Paths{Config: abs, Runtime: runtimeDir, State: filepath.Join(runtimeDir, "state.json"),
		Logs: logs, Log: latestLog(logs, id), ID: id}, nil
}
func resolvePath(base, name string) string {
	if strings.HasPrefix(name, "~/") || strings.HasPrefix(name, `~\`) {
		if home, err := os.UserHomeDir(); err == nil {
			name = filepath.Join(home, name[2:])
		}
	}
	if !filepath.IsAbs(name) {
		name = filepath.Join(base, name)
	}
	return filepath.Clean(name)
}

// LoadConfig validates TOML and resolves aliases through the native ssh -G -F.
func LoadConfig(path string) (config *Config, loadErr error) {
	path, err := filepath.Abs(path)
	if err != nil {
		return nil, err
	}
	editFile := path
	defer func() {
		if loadErr != nil && editFile != "" {
			loadErr = &configFileError{path: editFile, err: loadErr}
		}
	}()
	c := &Config{Path: path, resolved: map[string]sshSettings{}}
	c.Logging.MaxLogFiles, c.Logging.MaxSizeMB = defaultMaxLogFiles, defaultMaxLogMB
	// Defaults also apply to existing installations whose TOML predates these
	// fields. Shared tunnel policy never needs to be repeated in host entries.
	c.SSH.ConnectTimeout, c.SSH.ConnectionAttempts = 10, 1
	c.SSH.ServerAliveInterval, c.SSH.ServerAliveCountMax = 15, 3
	meta, err := toml.DecodeFile(path, c)
	if err != nil {
		return nil, fmt.Errorf("read config %s: %w", path, err)
	}
	if unknown := meta.Undecoded(); len(unknown) > 0 {
		return nil, fmt.Errorf("unknown config setting(s): %v", unknown)
	}
	if c.Logging.MaxLogFiles < 1 {
		return nil, fmt.Errorf("logging.max_log_files must be at least 1")
	}
	if c.Logging.MaxSizeMB < 1 || c.Logging.MaxSizeMB > maxLogMB {
		return nil, fmt.Errorf("logging.max_size_mb must be in 1..%d", maxLogMB)
	}
	ip := net.ParseIP(c.Proxy.Bind)
	if ip == nil || !ip.IsLoopback() {
		return nil, fmt.Errorf("proxy.bind must be a numeric loopback address (for example 127.0.0.1)")
	}
	if !validPort(c.Proxy.SocksPort) || !validPort(c.Proxy.HTTPPort) || c.Proxy.SocksPort == c.Proxy.HTTPPort {
		return nil, fmt.Errorf("HTTP and SOCKS ports must be different and in 1..65535")
	}
	for name, value := range map[string]int{"ready_timeout_seconds": c.SSH.ReadyTimeout, "retry_delay_seconds": c.SSH.RetryDelay,
		"health.interval_seconds": c.Health.Interval, "health.timeout_seconds": c.Health.Timeout, "health.failures_before_failover": c.Health.Failures,
		"ssh.connect_timeout_seconds": c.SSH.ConnectTimeout, "ssh.connection_attempts": c.SSH.ConnectionAttempts,
		"ssh.server_alive_interval_seconds": c.SSH.ServerAliveInterval, "ssh.server_alive_count_max": c.SSH.ServerAliveCountMax} {
		if value < 1 || value > 3600 {
			return nil, fmt.Errorf("%s must be in 1..3600", name)
		}
	}
	if len(c.Health.Targets) == 0 {
		return nil, fmt.Errorf("health.targets needs at least one host:port")
	}
	for _, target := range c.Health.Targets {
		host, port, e := net.SplitHostPort(target)
		p, nerr := strconv.Atoi(port)
		if e != nil || nerr != nil || host == "" || !validPort(p) {
			return nil, fmt.Errorf("health target must be host:port: %s", target)
		}
	}
	if c.SSH.ConfigFile == "" || c.SSH.Executable == "" {
		return nil, fmt.Errorf("ssh.executable and ssh.config_file are required")
	}
	base := filepath.Dir(path)
	c.SSH.ConfigFile = resolvePath(base, c.SSH.ConfigFile)
	if strings.ContainsAny(c.SSH.Executable, `/\`) {
		c.SSH.Executable = resolvePath(base, c.SSH.Executable)
	}
	resolved, err := exec.LookPath(c.SSH.Executable)
	if err != nil {
		editFile = "" // Missing software is not an invalid SSH host configuration.
		return nil, fmt.Errorf("OpenSSH client not found: %w", err)
	}
	c.SSH.Executable = resolved
	editFile = c.SSH.ConfigFile
	if info, err := os.Stat(c.SSH.ConfigFile); err != nil || info.IsDir() {
		return nil, fmt.Errorf("SSH config file not found: %s", c.SSH.ConfigFile)
	}
	c.Endpoints, err = SSHAliases(c.SSH.ConfigFile)
	if err != nil {
		return nil, err
	}
	for _, alias := range c.Endpoints {
		settings, err := c.inspectSSH(alias)
		if err != nil {
			return nil, err
		}
		c.resolved[alias] = settings
	}
	return c, nil
}

var validAlias = regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9_.-]*$`)

// SSHAliases only enumerates explicit Host declarations in the selected file.
// Includes/Match still supply settings through native ssh -G/-F; wildcard and
// conditional rules do not declare connection candidates. No SSH values are
// interpreted here. The library owns syntax/quoting, and OpenSSH owns resolution.
func SSHAliases(path string) ([]string, error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer file.Close()
	cfg, err := sshconfig.Decode(file)
	if err != nil {
		return nil, fmt.Errorf("read SSH aliases in %s: %w", path, err)
	}
	var aliases []string
	seen := map[string]bool{}
	for _, host := range cfg.Hosts {
		if strings.HasPrefix(strings.TrimSpace(host.String()), "Match ") {
			continue
		}
		for _, pattern := range host.Patterns {
			alias := pattern.String()
			if strings.ContainsAny(alias, "*?!") || !host.Matches(alias) {
				continue
			}
			if !validAlias.MatchString(alias) {
				return nil, fmt.Errorf("invalid SSH alias %q in %s; use letters, digits, dots, underscores or hyphens", alias, path)
			}
			key := strings.ToLower(alias)
			if !seen[key] {
				aliases = append(aliases, alias)
				seen[key] = true
			}
		}
	}
	if len(aliases) == 0 {
		return nil, fmt.Errorf("no explicit Host aliases in %s; add your hosts to ssh_config in connection priority order", path)
	}
	return aliases, nil
}
func validPort(p int) bool { return p > 0 && p <= 65535 }
func (c *Config) socksAddr() string {
	return net.JoinHostPort(c.Proxy.Bind, strconv.Itoa(c.Proxy.SocksPort))
}
func (c *Config) httpAddr() string {
	return net.JoinHostPort(c.Proxy.Bind, strconv.Itoa(c.Proxy.HTTPPort))
}
func (c *Config) sshBaseArgs(trust bool) []string {
	mode, strict := "yes", "yes"
	if trust {
		mode, strict = "no", "ask"
	}
	// OpenSSH command-line options take precedence over per-host settings.
	// Host-key storage is entirely native: do not override UserKnownHostsFile
	// or GlobalKnownHostsFile, or create a separate application trust store.
	// Keep the child owned by the supervisor even when a user config enables
	// backgrounding or connection multiplexing. The user file holds endpoint
	// identity, native Include/ProxyJump and authentication settings.
	return []string{"-F", c.SSH.ConfigFile, "-T",
		"-o", "BatchMode=" + mode, "-o", "StrictHostKeyChecking=" + strict,
		"-o", "ConnectTimeout=" + strconv.Itoa(c.SSH.ConnectTimeout),
		"-o", "ConnectionAttempts=" + strconv.Itoa(c.SSH.ConnectionAttempts),
		"-o", "ServerAliveInterval=" + strconv.Itoa(c.SSH.ServerAliveInterval),
		"-o", "ServerAliveCountMax=" + strconv.Itoa(c.SSH.ServerAliveCountMax),
		"-o", "TCPKeepAlive=yes",
		"-o", "ForkAfterAuthentication=no", "-o", "ControlMaster=no", "-o", "ControlPath=none",
		"-o", "ExitOnForwardFailure=yes", "-o", "ForwardAgent=no", "-o", "RemoteCommand=none"}
}
func (c *Config) inspectSSH(alias string) (sshSettings, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, c.SSH.Executable, append(c.sshBaseArgs(false), "-G", alias)...)
	cmd.Dir = filepath.Dir(c.SSH.ConfigFile)
	HideChild(cmd)
	output, err := cmd.CombinedOutput()
	if err != nil {
		return sshSettings{}, fmt.Errorf("resolve SSH alias %s with -G -F: %w: %s", alias, err, strings.TrimSpace(string(output)))
	}
	values := map[string][]string{}
	for _, line := range strings.Split(string(output), "\n") {
		key, value, found := strings.Cut(strings.TrimSpace(line), " ")
		if found {
			values[key] = append(values[key], strings.TrimSpace(value))
		}
	}
	one := func(key string) string {
		if len(values[key]) == 0 {
			return ""
		}
		return values[key][0]
	}
	if one("hostname") == "" || one("user") == "" || one("port") == "" {
		return sshSettings{}, fmt.Errorf("OpenSSH did not resolve host/user/port for %s", alias)
	}
	if one("clearallforwardings") == "yes" {
		return sshSettings{}, fmt.Errorf("%s: ClearAllForwardings yes would disable SOCKS", alias)
	}
	forwards := values["dynamicforward"]
	if len(forwards) > 1 {
		return sshSettings{}, fmt.Errorf("%s: configure only one DynamicForward, or let config.toml supply it", alias)
	}
	if len(forwards) == 1 {
		host, port, err := net.SplitHostPort(forwards[0])
		if err != nil || !net.ParseIP(host).Equal(net.ParseIP(c.Proxy.Bind)) || port != strconv.Itoa(c.Proxy.SocksPort) {
			return sshSettings{}, fmt.Errorf("%s: SSH DynamicForward %q must match %s from config.toml; remove it to let the supervisor add -D", alias, forwards[0], c.socksAddr())
		}
	}
	return sshSettings{destination: one("user") + "@" + net.JoinHostPort(one("hostname"), one("port")), hasDynamicForward: len(forwards) == 1}, nil
}

// SSHCommand uses -F and the alias. SSH-relative paths resolve from its config directory.
func SSHCommand(c *Config, alias string, trust bool) *exec.Cmd {
	args := c.sshBaseArgs(trust)
	if trust {
		// The remote command needs no input. -n avoids a pending console read
		// keeping Windows SSH alive after a fast 'exit'; local SSH prompts use TTY.
		args = append(args, "-n", "-o", "ClearAllForwardings=yes", alias, "exit")
	} else {
		args = append(args, "-N")
		if !c.resolved[alias].hasDynamicForward {
			args = append(args, "-D", c.socksAddr())
		}
		args = append(args, alias)
	}
	cmd := exec.Command(c.SSH.Executable, args...)
	cmd.Dir = filepath.Dir(c.SSH.ConfigFile)
	return cmd
}
