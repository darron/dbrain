package app

import (
	"fmt"
	"os/exec"
	"runtime"
	"strconv"
)

var startKeepAwake = startCaffeinate
var keepAwakeAvailable = canStartCaffeinate

func canStartCaffeinate() bool {
	if runtime.GOOS != "darwin" {
		return false
	}
	_, err := exec.LookPath("caffeinate")
	return err == nil
}

func startCaffeinate(pid int) error {
	if runtime.GOOS != "darwin" {
		return fmt.Errorf("--caffeinate is only supported on macOS")
	}

	bin, err := exec.LookPath("caffeinate")
	if err != nil {
		return fmt.Errorf("find caffeinate: %w", err)
	}

	cmd := exec.Command(bin, "-i", "-w", strconv.Itoa(pid))
	if err := cmd.Start(); err != nil {
		return fmt.Errorf("start caffeinate: %w", err)
	}

	go func() {
		_ = cmd.Wait()
	}()

	return nil
}
