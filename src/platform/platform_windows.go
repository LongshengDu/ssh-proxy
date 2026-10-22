package platform

import (
	"errors"
	"fmt"
	"io"
	"os/exec"
	"syscall"
	"unsafe"

	"golang.org/x/sys/windows"
)

func LockInstance(id string) (func(), error) {
	name, _ := windows.UTF16PtrFromString(`Global\ssh_proxy_` + id)
	h, err := windows.CreateMutex(nil, false, name)
	if err != nil {
		if h != 0 {
			windows.CloseHandle(h)
		}
		return nil, fmt.Errorf("another instance holds this configuration lock: %w", err)
	}
	return func() { windows.CloseHandle(h) }, nil
}

func HideChild(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true, CreationFlags: windows.CREATE_NO_WINDOW | windows.CREATE_NEW_PROCESS_GROUP}
}

// StartDetached starts a hidden background process outside the caller's job,
// so closing a terminal or IDE that owns a kill-on-close job does not stop it.
// A job that forbids breakaway rejects the flag; then start inside that job.
func StartDetached(newCmd func() *exec.Cmd) (*exec.Cmd, error) {
	cmd := newCmd()
	HideChild(cmd)
	cmd.SysProcAttr.CreationFlags |= windows.CREATE_BREAKAWAY_FROM_JOB
	err := cmd.Start()
	if errors.Is(err, windows.ERROR_ACCESS_DENIED) {
		cmd = newCmd()
		HideChild(cmd)
		err = cmd.Start()
	}
	return cmd, err
}

// ManagedCommand returns native SSH unchanged; ContainChild's job owns it.
func ManagedCommand(native *exec.Cmd) (*exec.Cmd, io.Closer, error) {
	return native, nil, nil
}

// Closing this handle also kills its SSH child if the supervisor crashes.
func ContainChild(cmd *exec.Cmd) (func(), error) {
	job, err := windows.CreateJobObject(nil, nil)
	if err != nil {
		return nil, err
	}
	info := windows.JOBOBJECT_EXTENDED_LIMIT_INFORMATION{}
	info.BasicLimitInformation.LimitFlags = windows.JOB_OBJECT_LIMIT_KILL_ON_JOB_CLOSE
	_, err = windows.SetInformationJobObject(job, windows.JobObjectExtendedLimitInformation, uintptr(unsafe.Pointer(&info)), uint32(unsafe.Sizeof(info)))
	if err != nil {
		windows.CloseHandle(job)
		return nil, err
	}
	h, err := windows.OpenProcess(windows.PROCESS_SET_QUOTA|windows.PROCESS_TERMINATE, false, uint32(cmd.Process.Pid))
	if err != nil {
		windows.CloseHandle(job)
		return nil, err
	}
	defer windows.CloseHandle(h)
	if err = windows.AssignProcessToJobObject(job, h); err != nil {
		windows.CloseHandle(job)
		return nil, err
	}
	return func() { windows.CloseHandle(job) }, nil
}

func ProcessAlive(pid int) bool {
	h, err := windows.OpenProcess(windows.PROCESS_QUERY_LIMITED_INFORMATION, false, uint32(pid))
	if err != nil {
		return false
	}
	defer windows.CloseHandle(h)
	var code uint32
	return windows.GetExitCodeProcess(h, &code) == nil && code == 259
}
