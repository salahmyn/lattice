//go:build !unix

package breaktest

// @module-feature breaktest

import (
	"os/exec"
	"time"
)

func setProcessGroup(cmd *exec.Cmd) { cmd.WaitDelay = 5 * time.Second }
