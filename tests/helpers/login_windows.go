//go:build systemtest

package testutil

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"golang.org/x/sys/windows"
	"golang.org/x/sys/windows/registry"
	"ssh_proxy/src/platform"
)

// Clean only abandoned entries from this repository's test folder. Existing
// executables (including another active test) and other installations stay intact.
func CleanupTestLogins() error {
	key, err := registry.OpenKey(registry.CURRENT_USER, `Software\Microsoft\Windows\CurrentVersion\Run`, registry.QUERY_VALUE|registry.SET_VALUE)
	if err == registry.ErrNotExist {
		return nil
	}
	if err != nil {
		return err
	}
	defer key.Close()
	names, err := key.ReadValueNames(0)
	if err != nil {
		return err
	}
	root := strings.ToLower(filepath.Join(Root(), "build", ".tmp")) + string(os.PathSeparator)
	for _, name := range names {
		id, kind := strings.TrimPrefix(name, "SSHProxy-"), "App-"
		if strings.HasPrefix(name, "SSHProxyCLI-") {
			id, kind = strings.TrimPrefix(name, "SSHProxyCLI-"), "CLI-"
		} else if id == name {
			continue
		}
		line, _, err := key.GetStringValue(name)
		if err != nil {
			continue
		}
		args, err := windows.DecomposeCommandLine(line)
		if err != nil || len(args) == 0 {
			continue
		}
		exe := args[0]
		if strings.EqualFold(filepath.Base(exe), "powershell.exe") {
			for _, suffix := range []string{kind + id, id} {
				settings, err := registry.OpenKey(registry.CURRENT_USER, `Software\SSHProxy\`+suffix, registry.QUERY_VALUE)
				if err != nil {
					continue
				}
				target, _, e := settings.GetStringValue("Executable")
				settings.Close()
				if e == nil {
					exe = target
					break
				}
			}
		}
		abs, err := filepath.Abs(exe)
		if err != nil || !strings.HasPrefix(strings.ToLower(abs), root) {
			continue
		}
		if _, err := os.Stat(abs); !os.IsNotExist(err) {
			continue
		}
		if err := (platform.LoginEntry{Name: name, Key: kind + id}).Disable(); err != nil {
			return err
		}
		if err := platform.RemoveLegacyLoginSettings(id); err != nil {
			return err
		}
	}
	return nil
}

func StartRegisteredProxy(t *testing.T, entry platform.LoginEntry) {
	t.Helper()
	key, err := registry.OpenKey(registry.CURRENT_USER, `Software\Microsoft\Windows\CurrentVersion\Run`, registry.QUERY_VALUE)
	if err != nil {
		t.Fatal(err)
	}
	defer key.Close()
	line, _, err := key.GetStringValue(entry.Name)
	if err != nil {
		t.Fatal(err)
	}
	args, err := windows.DecomposeCommandLine(line)
	if err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command(args[0], args[1:]...)
	// The launcher exits, including closing its console. Its supervisor must survive.
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatal(err, string(out))
	}
}
