package xmediatranscribe

import (
	"bytes"
	"context"
	"fmt"
	"os/exec"
	"strings"
)

func fileHasAudio(ctx context.Context, ffprobeBinary string, path string) (bool, error) {
	cmd := exec.CommandContext(ctx, ffprobeBinary,
		"-v", "error",
		"-select_streams", "a",
		"-show_entries", "stream=index",
		"-of", "csv=p=0",
		path,
	)
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		errMsg := strings.TrimSpace(stderr.String())
		if errMsg == "" {
			errMsg = err.Error()
		}
		return false, fmt.Errorf("ffprobe: %s", errMsg)
	}
	return strings.TrimSpace(stdout.String()) != "", nil
}
