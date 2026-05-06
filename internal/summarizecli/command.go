package summarizecli

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"time"
)

func runCommand(ctx context.Context, opts Options, args []string) ([]byte, error) {
	timeoutCtx, cancel := context.WithTimeout(ctx, opts.Timeout)
	defer cancel()

	delay := commandRetryDelay

	for attempt := 0; ; attempt++ {
		cmd := exec.CommandContext(timeoutCtx, opts.Binary, args...)
		configureCommandForCancellation(cmd)
		var stdout bytes.Buffer
		var stderr bytes.Buffer
		cmd.Stdout = &stdout
		cmd.Stderr = &stderr
		if len(opts.Env) > 0 {
			env := os.Environ()
			for key, value := range opts.Env {
				env = append(env, key+"="+value)
			}
			cmd.Env = env
		}
		if opts.Stdin != "" {
			cmd.Stdin = strings.NewReader(opts.Stdin)
		}

		if err := cmd.Run(); err != nil {
			errMsg := strings.TrimSpace(stderr.String())
			if errMsg == "" {
				errMsg = err.Error()
			}
			if !isRetryableCommandError(errMsg) || attempt >= commandRetryAttempts-1 {
				return nil, fmt.Errorf("run summarize: %s", errMsg)
			}
			if err := sleepWithContext(timeoutCtx, delay); err != nil {
				return nil, err
			}
			delay *= 2
			if delay > commandRetryMaxDelay {
				delay = commandRetryMaxDelay
			}
			continue
		}

		return stdout.Bytes(), nil
	}
}

func isRetryableCommandError(message string) bool {
	value := strings.ToLower(strings.TrimSpace(message))
	return strings.Contains(value, "sqlite_busy") ||
		strings.Contains(value, "sqlite_locked") ||
		strings.Contains(value, "database is locked") ||
		strings.Contains(value, "database table is locked")
}

func sleepWithContext(ctx context.Context, d time.Duration) error {
	timer := time.NewTimer(d)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}

func formatTimeout(value time.Duration) string {
	seconds := int(value.Seconds())
	if seconds <= 0 {
		seconds = 1
	}
	return fmt.Sprintf("%ds", seconds)
}
