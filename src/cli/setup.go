package cli

import (
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"

	"ssh-proxy/src/config"
)

// setupLogin guides one alias to unattended key login, skipping steps that are
// already done. It asks nothing itself; only OpenSSH prompts, for the host
// fingerprint and the server password, so it also works in the tray's terminal.
func setupLogin(cfg *config.Config, alias string, ssh func(*exec.Cmd) error, out io.Writer) error {
	ready := func() bool { return ssh(config.SSHCommand(cfg, alias, config.SSHCheck)) == nil }
	fmt.Fprintf(out, "Checking SSH login for %s.\n", alias)
	if ready() {
		fmt.Fprintf(out, "%s already logs in without prompts.\n", alias)
		return nil
	}
	key, found, err := config.ChooseIdentity(cfg.IdentityFiles(alias), func(path string) bool { _, err := os.Stat(path); return err == nil })
	if err != nil {
		return err
	}
	if !found {
		if err := createKey(cfg, key, out); err != nil {
			fmt.Fprintf(out, "Cannot create an SSH key: %v\n", err)
			key = ""
		}
	}
	public := ""
	if key != "" {
		public = publicKey(cfg, key)
	}
	fmt.Fprintf(out, "SSH login for %s. Verify the host fingerprint with your administrator before accepting; enter your server password if asked.\n", alias)
	login := config.SSHCommand(cfg, alias, config.SSHTrust)
	if public != "" {
		if cmd, err := config.SSHSetupCommand(cfg, alias, public); err == nil {
			fmt.Fprintf(out, "Adding your public key %s.pub to the server.\n", key)
			login = cmd
		}
	}
	if err := ssh(login); err != nil {
		return setupHelp(fmt.Errorf("SSH login for %s: %w", alias, err), public, out)
	}
	if ready() {
		fmt.Fprintf(out, "Ready: %s logs in without prompts. You can start the proxy.\n", alias)
		return nil
	}
	if key != "" && passphraseProtected(cfg, key) {
		fmt.Fprintf(out, "%s has a passphrase. Loading it into ssh-agent; enter the passphrase if asked.\n", key)
		if agent, err := cfg.SSHTool("ssh-add"); err == nil && runTool(agent, key) == nil && ready() {
			fmt.Fprintf(out, "Ready: %s logs in without prompts. You can start the proxy.\n", alias)
			return nil
		}
		if runtime.GOOS == "windows" {
			fmt.Fprintln(out, "Start ssh-agent once in an administrator PowerShell: Set-Service -Name ssh-agent -StartupType Automatic; Start-Service ssh-agent. Then run login again.")
		} else {
			fmt.Fprintln(out, `Start ssh-agent (eval "$(ssh-agent -s)") and run ssh-add, then run login again.`)
		}
	}
	return setupHelp(fmt.Errorf("%s still needs a password or passphrase to log in", alias), public, out)
}

// createKey makes an ed25519 key without a passphrase, so the background proxy
// needs no ssh-agent. It never overwrites an existing file.
func createKey(cfg *config.Config, key string, out io.Writer) error {
	keygen, err := cfg.SSHTool("ssh-keygen")
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(key), 0700); err != nil {
		return err
	}
	if err := runTool(keygen, "-q", "-t", "ed25519", "-N", "", "-f", key); err != nil {
		return err
	}
	fmt.Fprintf(out, "Created SSH key %s without a passphrase. To add one later, run ssh-keygen -p -f %s and load it with ssh-add.\n", key, key)
	return nil
}

// publicKey reads key.pub, or derives it from a key without a passphrase.
func publicKey(cfg *config.Config, key string) string {
	if data, err := os.ReadFile(key + ".pub"); err == nil {
		return strings.TrimSpace(strings.SplitN(string(data), "\n", 2)[0])
	}
	keygen, err := cfg.SSHTool("ssh-keygen")
	if err != nil {
		return ""
	}
	data, err := exec.Command(keygen, "-y", "-P", "", "-f", key).Output()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(data))
}

func passphraseProtected(cfg *config.Config, key string) bool {
	keygen, err := cfg.SSHTool("ssh-keygen")
	return err == nil && exec.Command(keygen, "-y", "-P", "", "-f", key).Run() != nil
}

func runTool(path string, args ...string) error {
	cmd := exec.Command(path, args...)
	cmd.Stdin, cmd.Stdout, cmd.Stderr = os.Stdin, os.Stdout, os.Stderr
	return cmd.Run()
}

func setupHelp(err error, public string, out io.Writer) error {
	if public != "" {
		fmt.Fprintf(out, "If the server doesn't accept password login, send this public key to your administrator:\n%s\n", public)
	}
	return err
}
