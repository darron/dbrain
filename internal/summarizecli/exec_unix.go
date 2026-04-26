//go:build darwin || linux

package summarizecli

import (
	"errors"
	"os"
	"os/exec"
	"syscall"
	"time"
)

func configureCommandForCancellation(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	cmd.WaitDelay = time.Second
	cmd.Cancel = func() error {
		if cmd.Process == nil {
			return nil
		}
		pgid, err := syscall.Getpgid(cmd.Process.Pid)
		if err == nil && pgid > 0 {
			if killErr := syscall.Kill(-pgid, syscall.SIGKILL); killErr == nil || errors.Is(killErr, syscall.ESRCH) {
				return nil
			} else {
				return killErr
			}
		}
		if err := cmd.Process.Kill(); err != nil && !errors.Is(err, os.ErrProcessDone) {
			return err
		}
		return nil
	}
}
