package summarizecli

import (
	"context"
	"errors"
	"os/exec"
	"strings"
	"time"
)

const versionProbeTimeout = 15 * time.Second

func Version(ctx context.Context, binary string) string {
	value := strings.TrimSpace(binary)
	if value == "" {
		value = "summarize"
	}

	versionMu.Lock()
	cached, ok := versionCache[value]
	versionMu.Unlock()
	if ok {
		return cached
	}

	version := detectVersion(ctx, value)
	if version == "" {
		return ""
	}

	versionMu.Lock()
	versionCache[value] = version
	versionMu.Unlock()
	return version
}

func detectVersion(ctx context.Context, binary string) string {
	timeoutCtx, cancel := context.WithTimeout(ctx, versionProbeTimeout)
	defer cancel()

	for _, args := range [][]string{{"--version"}, {"version"}} {
		cmd := exec.CommandContext(timeoutCtx, binary, args...)
		configureCommandForCancellation(cmd)
		output, err := cmd.Output()
		if err != nil {
			var exitErr *exec.ExitError
			if errors.As(err, &exitErr) {
				output = exitErr.Stderr
			} else {
				continue
			}
		}
		if len(output) == 0 {
			continue
		}
		value := strings.TrimSpace(string(output))
		if value == "" {
			continue
		}
		if idx := strings.IndexByte(value, '\n'); idx >= 0 {
			value = strings.TrimSpace(value[:idx])
		}
		return value
	}

	return ""
}
