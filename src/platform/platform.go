// Package platform holds the OS-specific process, lock and child ownership code.
package platform

// CloseJob releases a child's containment, if any.
func CloseJob(close func()) {
	if close != nil {
		close()
	}
}
