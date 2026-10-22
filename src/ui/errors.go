//go:build windows || darwin

package ui

import (
	"fmt"
	"os"
)

// Errors never allocate a log. Always show and open the process's existing file.
func showError(err error, logPath string) {
	if logPath == "" {
		_ = errorMessage(err.Error())
		return
	}
	message := err.Error() + "\n\nLog: " + logPath
	if _, statErr := os.Stat(logPath); statErr == nil {
		message += "\nThe log will open after you close this message."
		_ = errorMessage(message)
		if openErr := openPath(logPath); openErr != nil {
			_ = errorMessage(fmt.Sprintf("%v\n\nOpen this log manually:\n%s", openErr, logPath))
		}
	} else {
		_ = errorMessage(message + "\nThe log file could not be opened: " + statErr.Error())
	}
}
