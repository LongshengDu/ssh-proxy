//go:build darwin || linux

package platform

import (
	"errors"
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
		return nil, ErrAlreadyRunning
	}
	return func() { lock.Close() }, nil
}

// InstanceRunning probes the installation lock. Holding it even briefly could
// make a starting supervisor exit, so only stop uses it, never status polling.
func InstanceRunning(id string) (bool, error) {
	unlock, err := LockInstance(id)
	if errors.Is(err, ErrAlreadyRunning) {
		return true, nil
	}
	if err != nil {
		return false, err
	}
	unlock()
	return false, nil
}

// SupervisorAlive classifies a supervisor whose control API does not answer.
// Unix PIDs are not reused quickly, and status must not probe the lock.
func SupervisorAlive(_ string, pid int) bool { return ProcessAlive(pid) }

func HideChild(cmd *exec.Cmd) {}

// ProtectLog uses an advisory shared lock; logging and ssh -E can still append.
func ProtectLog(path string) (func(), error) {
	lock := flock.New(path, flock.SetFlag(os.O_RDONLY))
	if err := lock.RLock(); err != nil {
		lock.Close()
		return nil, err
	}
	return func() { lock.Close() }, nil
}

// RemoveInactiveLog leaves files held by any running log writer alone.
func RemoveInactiveLog(path string) (bool, error) {
	lock := flock.New(path, flock.SetFlag(os.O_RDONLY))
	defer lock.Close()
	ok, err := lock.TryLock()
	if err != nil || !ok {
		return false, err
	}
	return true, os.Remove(path)
}

// StartDetached starts a background process in its own session.
func StartDetached(newCmd func() *exec.Cmd) (*exec.Cmd, error) {
	cmd := newCmd()
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	return cmd, cmd.Start()
}
func ContainChild(cmd *exec.Cmd) (func(), error) {
	return func() { cmd.Process.Signal(syscall.SIGTERM) }, nil
}
func ProcessAlive(pid int) bool { return pid > 0 && syscall.Kill(pid, 0) == nil }

type parentLease struct{ reader, writer *os.File }

func (p *parentLease) Close() error { p.reader.Close(); return p.writer.Close() }

// ManagedCommand runs native SSH under this executable's _ssh guardian. A pipe
// lease lets the guardian stop SSH even if an unmanaged supervisor crashes.
// Native SSH inherits neither end of the lease. OS services also own the process group.
func ManagedCommand(native *exec.Cmd) (*exec.Cmd, io.Closer, error) {
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
