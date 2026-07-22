//go:build windows

package vaultfs

import (
	"fmt"
	"os"
)

func openRootNoFollow(string) (*os.Root, error) {
	return nil, fmt.Errorf("deep audit private temporary directories are not supported on Windows")
}

func privateCreateFlags() int { return os.O_CREATE | os.O_EXCL | os.O_WRONLY }
func privateOpenFlags() int   { return os.O_RDONLY }

func availableBytes(*os.Root) (uint64, error) {
	return 0, fmt.Errorf("deep audit private temporary directories are not supported on Windows")
}
