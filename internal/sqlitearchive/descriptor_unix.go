//go:build unix

package sqlitearchive

import (
	"fmt"
	"os"
	"runtime"
)

func descriptorPath(file *os.File) (string, error) {
	if runtime.GOOS == "linux" {
		return fmt.Sprintf("/proc/self/fd/%d", file.Fd()), nil
	}
	return fmt.Sprintf("/dev/fd/%d", file.Fd()), nil
}
