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

func transcribeWithMacWhisper(ctx context.Context, mediaPath string, opts Options) (string, error) {
	args := []string{"transcribe"}
	if modelID := strings.TrimSpace(opts.MacWhisperModel); modelID != "" {
		args = append(args, "--model", modelID)
	}
	args = append(args, mediaPath)

	cmd := exec.CommandContext(ctx, opts.MacWhisperBinary, args...)
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		errMsg := strings.TrimSpace(stderr.String())
		if errMsg == "" {
			errMsg = err.Error()
		}
		return "", fmt.Errorf("macwhisper transcription: %s", errMsg)
	}

	transcript := strings.TrimSpace(stdout.String())
	if transcript == "" {
		return "", fmt.Errorf("macwhisper transcript empty")
	}
	return transcript, nil
}
