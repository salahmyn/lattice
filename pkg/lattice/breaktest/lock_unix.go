//go:build unix

package breaktest

// @module-feature breaktest

import (
	"errors"
	"os"
	"syscall"
)

// processAlive reports whether a process with this pid exists on this
// machine, by sending it signal 0. A process owned by another user answers
// EPERM and is alive.
func processAlive(pid int) bool {
	p, err := os.FindProcess(pid)
	if err != nil {
		return false
	}
	err = p.Signal(syscall.Signal(0))
	return err == nil || errors.Is(err, syscall.EPERM)
}
