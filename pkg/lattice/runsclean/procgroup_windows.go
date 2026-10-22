//go:build windows

package runsclean

import "os/exec"

func startGroup(c *exec.Cmd) {}

// shortcut: kills only the boot process, its children may outlive it; use a job object if Windows users need the full tree stopped.
func stopGroup(c *exec.Cmd, force bool) { _ = c.Process.Kill() }
