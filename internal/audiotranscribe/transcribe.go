package audiotranscribe

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

const (
	BackendAuto       = "auto"
	BackendWhisperCPP = "whisper.cpp"
	BackendMacWhisper = "macwhisper"
)

type Config struct {
	Backend             string
	Language            string
	WhisperBinary       string
	WhisperModelPath    string
	WhisperVADModelPath string
	MacWhisperBinary    string
	MacWhisperModel     string
}

type Result struct {
	Text       string
	Backend    string
	Model      string
	Language   string
	VADEnabled bool
	NoSpeech   bool
}

func Transcribe(ctx context.Context, audioPath string, cfg Config) (Result, error) {
	cfg = normalizeConfig(cfg)
	backend, model, err := resolveBackend(cfg)
	if err != nil {
		return Result{}, err
	}
	switch backend {
	case BackendWhisperCPP:
		return transcribeWhisperCPP(ctx, audioPath, cfg)
	case BackendMacWhisper:
		cfg.MacWhisperModel = model
		return transcribeMacWhisper(ctx, audioPath, cfg)
	default:
		return Result{}, fmt.Errorf("unsupported transcription backend %q", backend)
	}
}

func normalizeConfig(cfg Config) Config {
	cfg.Backend = strings.TrimSpace(cfg.Backend)
	if cfg.Backend == "" {
		cfg.Backend = BackendAuto
	}
	cfg.Language = strings.TrimSpace(cfg.Language)
	if cfg.Language == "" {
		cfg.Language = "auto"
	}
	cfg.WhisperBinary = strings.TrimSpace(cfg.WhisperBinary)
	if cfg.WhisperBinary == "" {
		cfg.WhisperBinary = "whisper-cli"
	}
	cfg.MacWhisperBinary = strings.TrimSpace(cfg.MacWhisperBinary)
	if cfg.MacWhisperBinary == "" {
		cfg.MacWhisperBinary = "mw"
	}
	return cfg
}

func resolveBackend(cfg Config) (string, string, error) {
	selector := strings.ToLower(strings.TrimSpace(cfg.Backend))
	switch {
	case selector == BackendAuto:
		if whisperCPPAvailable(cfg) {
			return BackendWhisperCPP, filepath.Base(cfg.WhisperModelPath), nil
		}
		if _, err := exec.LookPath(cfg.MacWhisperBinary); err == nil {
			return BackendMacWhisper, strings.TrimSpace(cfg.MacWhisperModel), nil
		}
		return "", "", errors.New("no local transcription backend is ready: configure whisper.cpp model paths or install MacWhisper")
	case selector == BackendWhisperCPP, selector == "whisper-cpp", selector == "whispercpp":
		return BackendWhisperCPP, filepath.Base(cfg.WhisperModelPath), nil
	case selector == BackendMacWhisper:
		return BackendMacWhisper, strings.TrimSpace(cfg.MacWhisperModel), nil
	case strings.HasPrefix(selector, BackendMacWhisper+":"):
		return BackendMacWhisper, strings.TrimSpace(cfg.Backend[len(BackendMacWhisper)+1:]), nil
	default:
		return "", "", fmt.Errorf("unsupported transcription backend %q (use auto, whisper.cpp, or macwhisper[:model])", cfg.Backend)
	}
}

func whisperCPPAvailable(cfg Config) bool {
	if strings.TrimSpace(cfg.WhisperModelPath) == "" {
		return false
	}
	if _, err := os.Stat(cfg.WhisperModelPath); err != nil {
		return false
	}
	_, err := exec.LookPath(cfg.WhisperBinary)
	return err == nil
}

func transcribeWhisperCPP(ctx context.Context, audioPath string, cfg Config) (Result, error) {
	if strings.TrimSpace(cfg.WhisperModelPath) == "" {
		return Result{}, errors.New("whisper.cpp model path is not configured")
	}
	if err := requireFile(cfg.WhisperModelPath, "whisper.cpp model"); err != nil {
		return Result{}, err
	}
	vadEnabled := false
	if cfg.WhisperVADModelPath != "" {
		if info, err := os.Stat(cfg.WhisperVADModelPath); err == nil && !info.IsDir() {
			vadEnabled = true
		}
	}

	tempDir, err := os.MkdirTemp("", "dbrain-whisper-")
	if err != nil {
		return Result{}, fmt.Errorf("create whisper output dir: %w", err)
	}
	defer func() { _ = os.RemoveAll(tempDir) }()
	outputBase := filepath.Join(tempDir, "transcript")
	args := []string{
		"-m", cfg.WhisperModelPath,
		"-l", cfg.Language,
		"-nt",
		"-np",
	}
	if vadEnabled {
		args = append(args, "--vad", "--vad-model", cfg.WhisperVADModelPath)
	}
	args = append(args, "-otxt", "-of", outputBase, "-f", audioPath)

	cmd := exec.CommandContext(ctx, cfg.WhisperBinary, args...)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return Result{}, commandError("whisper.cpp transcription", err, stderr.String())
	}
	raw, err := os.ReadFile(outputBase + ".txt")
	if err != nil {
		return Result{}, fmt.Errorf("read whisper.cpp transcript: %w", err)
	}
	text, noSpeech := normalizeTranscript(string(raw))
	return Result{
		Text:       text,
		Backend:    BackendWhisperCPP,
		Model:      filepath.Base(cfg.WhisperModelPath),
		Language:   cfg.Language,
		VADEnabled: vadEnabled,
		NoSpeech:   noSpeech,
	}, nil
}

func transcribeMacWhisper(ctx context.Context, audioPath string, cfg Config) (Result, error) {
	args := []string{"transcribe"}
	if cfg.MacWhisperModel != "" {
		args = append(args, "--model", cfg.MacWhisperModel)
	}
	args = append(args, audioPath)
	cmd := exec.CommandContext(ctx, cfg.MacWhisperBinary, args...)
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return Result{}, commandError("macwhisper transcription", err, stderr.String())
	}
	text, noSpeech := normalizeTranscript(stdout.String())
	return Result{
		Text:     text,
		Backend:  BackendMacWhisper,
		Model:    cfg.MacWhisperModel,
		Language: "auto",
		NoSpeech: noSpeech,
	}, nil
}

func normalizeTranscript(value string) (string, bool) {
	text := strings.TrimSpace(value)
	switch strings.ToLower(text) {
	case "", "[blank_audio]", "[blank audio]":
		return "", true
	case "[music]", "[musik]":
		return text, true
	default:
		return text, false
	}
}

func requireFile(path string, label string) error {
	info, err := os.Stat(path)
	if err != nil {
		return fmt.Errorf("%s missing: %w", label, err)
	}
	if info.IsDir() {
		return fmt.Errorf("%s path is a directory: %s", label, path)
	}
	return nil
}

func commandError(label string, runErr error, stderr string) error {
	message := strings.TrimSpace(stderr)
	if message == "" {
		message = runErr.Error()
	}
	return fmt.Errorf("%s: %s", label, message)
}
