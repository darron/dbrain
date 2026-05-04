package sourceenrich

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

func transcribeAudioFile(ctx context.Context, audioPath string, opts Options) (string, string, error) {
	if shouldUseMacWhisper(opts.YouTubeTranscriber, opts.MacWhisperBinary) {
		transcript, err := transcribeAudioWithMacWhisper(ctx, audioPath, opts)
		if err == nil {
			return transcript, "macwhisper", nil
		}
		if explicitMacWhisper(opts.YouTubeTranscriber) {
			return "", "", err
		}
		debugLog(opts.Logger, "macwhisper transcription failed; falling back to whisper.cpp", "audio_path", audioPath, "error", err.Error())
	}

	transcript, err := transcribeAudioWithWhisperCLI(ctx, audioPath, opts)
	if err != nil {
		return "", "", err
	}
	return transcript, "whisper.cpp", nil
}

func transcribeAudioWithWhisperCLI(ctx context.Context, audioPath string, opts Options) (string, error) {
	if strings.TrimSpace(opts.WhisperModelPath) == "" {
		return "", fmt.Errorf("whisper model path not configured")
	}
	if _, err := os.Stat(opts.WhisperModelPath); err != nil {
		return "", fmt.Errorf("whisper model missing: %w", err)
	}

	outputBase := filepath.Join(filepath.Dir(audioPath), "transcript")
	whisperArgs := []string{
		"-m", opts.WhisperModelPath,
		"-l", "auto",
		"-otxt",
		"-of", outputBase,
		"-f", audioPath,
		"-np",
		"-nt",
	}
	whisperCmd := exec.CommandContext(ctx, opts.WhisperBinary, whisperArgs...)
	var whisperStderr bytes.Buffer
	whisperCmd.Stderr = &whisperStderr
	if err := whisperCmd.Run(); err != nil {
		return "", fmt.Errorf("whisper transcription: %s", strings.TrimSpace(whisperStderr.String()))
	}

	transcriptBytes, err := os.ReadFile(outputBase + ".txt")
	if err != nil {
		return "", fmt.Errorf("read whisper transcript: %w", err)
	}
	transcript := strings.TrimSpace(string(transcriptBytes))
	if transcript == "" {
		return "", fmt.Errorf("whisper transcript empty")
	}
	return transcript, nil
}

func transcribeAudioWithMacWhisper(ctx context.Context, audioPath string, opts Options) (string, error) {
	args := []string{"transcribe"}
	if modelID := macWhisperModelOverride(opts.YouTubeTranscriber); modelID != "" {
		args = append(args, "--model", modelID)
	}
	args = append(args, audioPath)

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

func shouldUseMacWhisper(transcriber string, binary string) bool {
	if explicitMacWhisper(transcriber) {
		return true
	}
	if value := strings.TrimSpace(transcriber); value != "" && !strings.EqualFold(value, "auto") {
		return false
	}
	_, err := exec.LookPath(strings.TrimSpace(binary))
	return err == nil
}

func explicitMacWhisper(transcriber string) bool {
	value := strings.ToLower(strings.TrimSpace(transcriber))
	return value == "macwhisper" || strings.HasPrefix(value, "macwhisper:")
}

func macWhisperModelOverride(transcriber string) string {
	value := strings.TrimSpace(transcriber)
	if !strings.HasPrefix(strings.ToLower(value), "macwhisper:") {
		return ""
	}
	return strings.TrimSpace(value[len("macwhisper:"):])
}
