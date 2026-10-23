//go:build systemtest

package testutil

import (
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"ssh-proxy/src/platform"
)

// Clean only abandoned user units from this repository's test folder, which a
// run killed by its timeout leaves behind. Other installations stay intact.
func CleanupTestLogins() error {
	home, err := os.UserHomeDir()
	if err != nil {
		return err
	}
	units, err := filepath.Glob(filepath.Join(home, ".config", "systemd", "user", "ssh-proxy-*.service"))
	if err != nil {
		return err
	}
	root := filepath.Join(Root(), "build", ".tmp") + string(os.PathSeparator)
	for _, path := range units {
		data, err := os.ReadFile(path)
		_, line, found := strings.Cut(string(data), "\nExecStart=")
		quoted, quoteErr := strconv.QuotedPrefix(line)
		if err != nil || !found || quoteErr != nil {
			continue
		}
		exe, _ := strconv.Unquote(quoted)
		exe = strings.NewReplacer("%%", "%", "$$", "$").Replace(exe)
		if _, err := os.Stat(exe); !strings.HasPrefix(exe, root) || !os.IsNotExist(err) {
			continue
		}
		if err := (platform.LoginEntry{Name: strings.TrimSuffix(filepath.Base(path), ".service")}).Disable(); err != nil {
			return err
		}
	}
	return nil
}

func StartRegisteredProxy(t *testing.T, entry platform.LoginEntry) {
	t.Helper()
	t.Cleanup(func() { exec.Command("systemctl", "--user", "stop", entry.Name+".service").Run() })
	if out, err := exec.Command("systemctl", "--user", "start", entry.Name+".service").CombinedOutput(); err != nil {
		t.Fatal(err, string(out))
	}
}
