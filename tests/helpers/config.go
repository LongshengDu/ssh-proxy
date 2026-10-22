package testutil

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func Root() string {
	_, file, _, _ := runtime.Caller(0)
	return filepath.Clean(filepath.Join(filepath.Dir(file), "../.."))
}

func ConfigTemplate(t *testing.T) []byte {
	t.Helper()
	template, err := os.ReadFile(filepath.Join(Root(), "src/packaging/defaults/config.toml"))
	if err != nil {
		t.Fatal(err)
	}
	return template
}

func Config(t *testing.T, sshText string) (string, string) {
	t.Helper()
	dir := filepath.Join(t.TempDir(), "path with spaces")
	if err := os.MkdirAll(dir, 0700); err != nil {
		t.Fatal(err)
	}
	template := ConfigTemplate(t)
	configPath := filepath.Join(dir, "config.toml")
	if err := os.WriteFile(configPath, template, 0600); err != nil {
		t.Fatal(err)
	}
	sshPath := filepath.Join(dir, "ssh_config")
	if err := os.WriteFile(sshPath, []byte(sshText), 0600); err != nil {
		t.Fatal(err)
	}
	return configPath, sshPath
}

func EffectiveSSH(t *testing.T, cmd *exec.Cmd) string {
	t.Helper()
	cmd.Args = append(cmd.Args[:1], append([]string{"-G"}, cmd.Args[1:]...)...)
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("ssh -G failed: %v: %s", err, output)
	}
	return strings.ReplaceAll(string(output), "\r\n", "\n")
}
