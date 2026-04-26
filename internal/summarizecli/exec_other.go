//go:build !darwin && !linux

package summarizecli

import (
	"os/exec"
	"time"
)

func configureCommandForCancellation(cmd *exec.Cmd) {
	cmd.WaitDelay = time.Second
}
