package service

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	kardianos "github.com/kardianos/service"
)

func serviceStartupPath(id string) (string, error) {
	home, err := os.UserHomeDir()
	// Match kardianos/service's per-user unit location.
	return filepath.Join(home, ".config", "systemd", "user", serviceName(id)+".service"), err
}

func servicePresent(id string) (bool, error) {
	path, err := serviceStartupPath(id)
	if err != nil {
		return false, err
	}
	_, err = os.Stat(path)
	if os.IsNotExist(err) {
		return false, nil
	}
	return err == nil, err
}

func systemctl(args ...string) (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	out, err := exec.CommandContext(ctx, "systemctl", append([]string{"--user"}, args...)...).CombinedOutput()
	return strings.TrimSpace(string(out)), err
}

func configureService(cfg *kardianos.Config) error {
	system := kardianos.ChosenSystem()
	if system == nil || system.String() != "linux-systemd" {
		return fmt.Errorf("automatic startup requires user systemd; use start or start --foreground without it")
	}
	if out, err := systemctl("show-environment"); err != nil {
		return fmt.Errorf("user systemd is unavailable: %w: %s; use start --foreground or start without an installed service", err, out)
	}
	cfg.Option["UserService"] = true
	unit, err := SystemdScript(cfg)
	if err != nil {
		return err
	}
	cfg.Option["SystemdScript"] = unit
	return nil
}

// SystemdScript builds the per-user unit passed to kardianos/service.
func SystemdScript(cfg *kardianos.Config) (string, error) {
	// The library's default unit targets a system target and does not escape
	// environment values. Supply a user unit; reuse its install/control lifecycle.
	quote := func(value string) string { return strconv.Quote(strings.ReplaceAll(value, "%", "%%")) }
	args := []string{cfg.Executable}
	args = append(args, cfg.Arguments...)
	for i, arg := range args {
		args[i] = quote(strings.ReplaceAll(arg, "$", "$$"))
	}
	// All application/SSH paths are resolved explicitly; no WorkingDirectory is needed.
	unit := fmt.Sprintf(linuxUnit, strings.Join(args, " "), quote("SSH_PROXY_RUNTIME="+cfg.EnvVars["SSH_PROXY_RUNTIME"]))
	// The service library processes its own template syntax after this step.
	if strings.Contains(unit, "{{") {
		return "", fmt.Errorf("service paths cannot contain '{{'; choose another installation/config path")
	}
	return unit, nil
}

func grantServiceControl(string) error { return nil }

func ServiceAutoStart(name, action string) (bool, error) {
	if action != "status" {
		if out, err := systemctl(action, name+".service"); err != nil {
			return false, fmt.Errorf("systemctl %s: %w: %s", action, err, out)
		}
	}
	out, err := systemctl("is-enabled", name+".service")
	switch out {
	case "enabled", "enabled-runtime":
		return true, nil
	case "disabled", "masked", "masked-runtime", "static", "indirect", "not-found":
		return false, nil
	}
	return false, fmt.Errorf("systemctl is-enabled: %v: %s", err, out)
}

const linuxUnit = `[Unit]
Description=SSH Proxy

[Service]
ExecStart=%s
Restart=on-failure
RestartSec=5
TimeoutStopSec=25
KillMode=control-group
UMask=0077
Environment=%s
[Install]
WantedBy=default.target
`
