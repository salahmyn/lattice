//go:build unix

package breaktest

// @module-feature breaktest

import (
	"os/exec"
	"syscall"
	"time"
)

// setProcessGroup puts the command in its own process group and kills the
// whole group when its context ends.
func setProcessGroup(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	cmd.Cancel = func() error {
		return syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
	}
	cmd.WaitDelay = 5 * time.Second
}
