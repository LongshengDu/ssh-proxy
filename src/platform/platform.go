// Package platform holds the OS-specific process, lock and child ownership code.
package platform

import "errors"

var ErrAlreadyRunning = errors.New("already running; use status or stop")

const AlreadyRunningExitCode = 10

// CloseJob releases a child's containment, if any.
func CloseJob(close func()) {
	if close != nil {
		close()
	}
}
