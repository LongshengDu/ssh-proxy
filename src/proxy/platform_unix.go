//go:build darwin || linux

package proxy

import (
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"syscall"

	"github.com/gofrs/flock"
)

func LockInstance(id string) (func(), error) {
	base, err := os.UserCacheDir()
	if err != nil {
		return nil, err
	}
	dir := filepath.Join(base, "ssh_proxy", "locks")
	if err = os.MkdirAll(dir, 0700); err != nil {
		return nil, err
	}
	lock := flock.New(filepath.Join(dir, id+".lock"))
	ok, err := lock.TryLock()
	if err != nil {
		return nil, err
	}
	if !ok {
		return nil, fmt.Errorf("another instance holds this configuration lock")
	}
	return func() { lock.Close() }, nil
}
func HideChild(cmd *exec.Cmd)   {}
func DetachChild(cmd *exec.Cmd) { cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true} }
func ContainChild(cmd *exec.Cmd) (func(), error) {
	return func() { cmd.Process.Signal(syscall.SIGTERM) }, nil
}
func ProcessAlive(pid int) bool { return pid > 0 && syscall.Kill(pid, 0) == nil }

type parentLease struct{ reader, writer *os.File }

func (p *parentLease) Close() error { p.reader.Close(); return p.writer.Close() }

// A pipe lease lets the helper stop SSH even if an unmanaged supervisor crashes.
// Native SSH inherits neither end of the lease. OS services also own the process group.
func managedSSHCommand(c *Config, alias string) (*exec.Cmd, io.Closer, error) {
	native := SSHCommand(c, alias, false)
	exe, err := os.Executable()
	if err != nil {
		return nil, nil, err
	}
	reader, writer, err := os.Pipe()
	if err != nil {
		return nil, nil, err
	}
	cmd := exec.Command(exe, append([]string{"_ssh"}, native.Args...)...)
	cmd.Dir, cmd.Stdin = native.Dir, reader
	return cmd, &parentLease{reader, writer}, nil
}
