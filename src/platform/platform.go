// Package platform holds the OS-specific process, lock and child ownership code.
package platform

import (
	"errors"
	"io/fs"
	"os"
	"time"
)

var ErrAlreadyRunning = errors.New("already running; use status or stop")

const AlreadyRunningExitCode = 10

// CloseJob releases a child's containment, if any.
func CloseJob(close func()) {
	if close != nil {
		close()
	}
}

// ReplaceFile renames from over to. Windows refuses to replace or remove a file
// while another process reads it, such as a status poll, so retry briefly.
func ReplaceFile(from, to string) error {
	return retry(func() error { return os.Rename(from, to) })
}

// RemoveFile removes path, retrying like ReplaceFile. A missing file is not an error.
func RemoveFile(path string) error {
	err := retry(func() error { return os.Remove(path) })
	if errors.Is(err, fs.ErrNotExist) {
		return nil
	}
	return err
}

func retry(change func() error) error {
	err := change()
	for delay := 20 * time.Millisecond; err != nil && !errors.Is(err, fs.ErrNotExist) && delay <= 640*time.Millisecond; delay *= 2 {
		time.Sleep(delay)
		err = change()
	}
	return err
}
