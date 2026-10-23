package tests

import (
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"ssh_proxy/src/config"
	"ssh_proxy/tests/helpers"
)

// login uses an existing key, or creates one where OpenSSH looks without an
// IdentityFile line; a key path the user configured is respected, even when
// its name is one of OpenSSH's default names.
func TestChooseIdentity(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	ssh := filepath.Join(home, ".ssh")
	defaults := []string{filepath.Join(ssh, "id_rsa"), filepath.Join(ssh, "id_ecdsa"), filepath.Join(ssh, "id_ed25519")}
	for _, tc := range []struct {
		name     string
		files    []string
		existing string
		want     string
		found    bool
	}{
		{"no keys, defaults", defaults, "", filepath.Join(ssh, "id_ed25519"), false},
		{"existing default", defaults, filepath.Join(ssh, "id_rsa"), filepath.Join(ssh, "id_rsa"), true},
		{"configured path", []string{filepath.Join(ssh, "work_key")}, "", filepath.Join(ssh, "work_key"), false},
		{"configured default name elsewhere", []string{filepath.Join(ssh, "keys", "id_rsa")}, "", filepath.Join(ssh, "keys", "id_rsa"), false},
		{"configured default path", []string{filepath.Join(ssh, "id_rsa")}, "", filepath.Join(ssh, "id_rsa"), false},
		{"nothing listed", nil, "", filepath.Join(ssh, "id_ed25519"), false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, found := config.ChooseIdentity(tc.files, func(path string) bool { return path == tc.existing })
			if got != tc.want || found != tc.found {
				t.Fatalf("got %s %v, want %s %v", got, found, tc.want, tc.found)
			}
		})
	}
}

// The server-side command accepts only an OpenSSH key line, quotes nothing from
// the key comment, and adds the key once however often trust runs.
func TestAuthorizeKeyCommand(t *testing.T) {
	const key = "ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAIExampleKeyData0123456789abcdef"
	for _, bad := range []string{"", "not a key", "ssh-ed25519 AAAA'; rm -rf ~ #", "ssh-ed25519 AA$(id)AA"} {
		if _, err := config.AuthorizeKeyCommand(bad); err == nil {
			t.Fatalf("accepted %q", bad)
		}
	}
	command, err := config.AuthorizeKeyCommand(key + " user@host with 'quotes'")
	if err != nil || strings.Contains(command, "quotes") {
		t.Fatal(command, err)
	}
	sh, err := exec.LookPath("sh")
	if err != nil {
		t.Skip("no POSIX shell to run the remote command")
	}
	// An existing file may not end with a newline; both keys must stay whole lines.
	for _, existing := range []string{"", "ssh-rsa AAAAB3NzaC1yc2EOldKey"} {
		home := t.TempDir()
		file := filepath.Join(home, ".ssh", "authorized_keys")
		if existing != "" {
			if err := os.MkdirAll(filepath.Dir(file), 0700); err != nil {
				t.Fatal(err)
			}
			writeText(t, file, existing)
		}
		for range 2 {
			cmd := exec.Command(sh, "-c", command)
			cmd.Env = append(os.Environ(), "HOME="+home)
			if out, err := cmd.CombinedOutput(); err != nil {
				t.Fatal(err, string(out))
			}
		}
		data, err := os.ReadFile(file)
		if err != nil || strings.Count(string(data), key) != 1 {
			t.Fatalf("key must be added exactly once: %v %q", err, data)
		}
		var lines []string
		for _, line := range strings.Split(string(data), "\n") {
			if line != "" {
				lines = append(lines, line)
			}
		}
		want := []string{key + " ssh_proxy"}
		if existing != "" {
			want = append([]string{existing}, want...)
		}
		if !slices.Equal(lines, want) {
			t.Fatalf("authorized_keys lines %q, want %q", lines, want)
		}
	}
}

// IdentityFile comes from OpenSSH's own resolution (ssh -G), relative to home.
func TestIdentityFilesFromSSH(t *testing.T) {
	path, _ := testutil.Config(t, "Host work\n HostName fixture.invalid\n User tester\n IdentityFile ~/.ssh/work_key\n")
	cfg, err := config.LoadConfig(path)
	if err != nil {
		t.Fatal(err)
	}
	home, _ := os.UserHomeDir()
	files := cfg.IdentityFiles("work")
	if len(files) == 0 || files[0] != filepath.Join(home, ".ssh", "work_key") {
		t.Fatal(files)
	}
}
