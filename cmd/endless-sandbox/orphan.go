package main

import (
	"errors"
	"syscall"
)

// isAlive reports whether the given pid refers to a running process.
// On Unix, syscall.Kill(pid, 0) returns:
//   - nil       process exists and we can signal it (alive)
//   - EPERM     process exists but we lack permission to signal it (alive)
//   - ESRCH     process does not exist (dead)
func isAlive(pid int) bool {
	if pid <= 0 {
		return false
	}
	err := syscall.Kill(pid, 0)
	if err == nil {
		return true
	}
	if errors.Is(err, syscall.EPERM) {
		return true
	}
	return false
}
