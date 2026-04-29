//go:build windows

package remote

import (
	"fmt"
	"os"
)

type StateLock struct {
	path string
}

func AcquireStateLock(stateDir string) (*StateLock, error) {
	return nil, fmt.Errorf("tsnet state locking is not supported on Windows yet: %s", stateDir)
}

func (l *StateLock) Close() error {
	return nil
}

func (l *StateLock) Path() string {
	if l == nil {
		return ""
	}
	return l.path
}

func checkCurrentUserOwns(os.FileInfo) error {
	return nil
}
