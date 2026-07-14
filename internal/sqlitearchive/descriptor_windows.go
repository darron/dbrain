//go:build windows

package sqlitearchive

import (
	"fmt"
	"os"
)

func descriptorPath(*os.File) (string, error) {
	return "", fmt.Errorf("deep audit descriptor-backed SQLite validation is not supported on Windows")
}
