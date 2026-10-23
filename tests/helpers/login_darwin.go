//go:build systemtest

package testutil

import (
	"fmt"
	"html"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"ssh-proxy/src/platform"
)

// Clean only abandoned LaunchAgents from this repository's test folder, which a
// run killed by its timeout leaves behind. Other installations stay intact.
func CleanupTestLogins() error {
	home, err := os.UserHomeDir()
	if err != nil {
		return err
	}
	plists, err := filepath.Glob(filepath.Join(home, "Library", "LaunchAgents", "com.sshproxy.*.plist"))
	if err != nil {
		return err
	}
	root := filepath.Join(Root(), "build", ".tmp") + string(os.PathSeparator)
	program := regexp.MustCompile(`<key>ProgramArguments</key><array><string>([^<]*)</string>`)
	for _, path := range plists {
		data, err := os.ReadFile(path)
		match := program.FindSubmatch(data)
		if err != nil || match == nil {
			continue
		}
		exe := html.UnescapeString(string(match[1]))
		if _, err := os.Stat(exe); !strings.HasPrefix(exe, root) || !os.IsNotExist(err) {
			continue
		}
		label := strings.TrimSuffix(filepath.Base(path), ".plist")
		exec.Command("launchctl", "bootout", fmt.Sprintf("gui/%d/%s", os.Getuid(), label)).Run()
		if err := (platform.LoginEntry{Name: label}).Disable(); err != nil {
			return err
		}
	}
	return nil
}

func StartRegisteredProxy(t *testing.T, entry platform.LoginEntry) {
	t.Helper()
	domain := fmt.Sprintf("gui/%d", os.Getuid())
	path, err := entry.Path()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { exec.Command("launchctl", "bootout", domain+"/"+entry.Name).Run() })
	if out, err := exec.Command("launchctl", "bootstrap", domain, path).CombinedOutput(); err != nil {
		t.Fatal(err, string(out))
	}
}
