//go:build !windows

package runsclean

import (
	"os/exec"
	"syscall"
)

// startGroup puts the command in its own process group, so stopGroup reaches
// the children boot commands commonly spawn (shells, dev servers).
func startGroup(c *exec.Cmd) { c.SysProcAttr = &syscall.SysProcAttr{Setpgid: true} }

// stopGroup signals the whole group (negative pid); force sends SIGKILL.
func stopGroup(c *exec.Cmd, force bool) {
	sig := syscall.SIGTERM
	if force {
		sig = syscall.SIGKILL
	}
	_ = syscall.Kill(-c.Process.Pid, sig)
}
