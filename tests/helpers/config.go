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
	template, err := os.ReadFile(filepath.Join(Root(), "src/packaging/defaults/support/config.toml.in"))
	if err != nil {
		t.Fatal(err)
	}
	return template
}

// Support adds the shipped support/*.in templates, which every install folder has.
func Support(t *testing.T, dir string) {
	t.Helper()
	templates, err := filepath.Glob(filepath.Join(Root(), "src", "packaging", "defaults", "support", "*.in"))
	if err != nil || len(templates) == 0 {
		t.Fatal("no support/*.in templates", err)
	}
	if err := os.MkdirAll(filepath.Join(dir, "support"), 0700); err != nil {
		t.Fatal(err)
	}
	for _, template := range templates {
		data, err := os.ReadFile(template)
		if err == nil {
			err = os.WriteFile(filepath.Join(dir, "support", filepath.Base(template)), data, 0600)
		}
		if err != nil {
			t.Fatal(err)
		}
	}
}

func Config(t *testing.T, sshText string) (string, string) {
	t.Helper()
	dir := filepath.Join(t.TempDir(), "path with spaces")
	Support(t, dir)
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
