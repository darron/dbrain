package sourceenrich

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/darron/dbrain/internal/config"
	"github.com/darron/dbrain/internal/model"
)

type youtubeExtractEnvelope struct {
	Extracted struct {
		TranscriptSource      *string `json:"transcriptSource"`
		TranscriptionProvider *string `json:"transcriptionProvider"`
		TranscriptCharacters  *int    `json:"transcriptCharacters"`
	} `json:"extracted"`
}

func shouldUseAudioTranscriptionFallback(extract model.ExtractResult) bool {
	var payload youtubeExtractEnvelope
	if err := json.Unmarshal([]byte(strings.TrimSpace(extract.RawJSON)), &payload); err != nil {
		return false
	}

	transcriptSource := ""
	if payload.Extracted.TranscriptSource != nil {
		transcriptSource = strings.TrimSpace(*payload.Extracted.TranscriptSource)
	}
	transcriptionProvider := ""
	if payload.Extracted.TranscriptionProvider != nil {
		transcriptionProvider = strings.TrimSpace(*payload.Extracted.TranscriptionProvider)
	}
	transcriptChars := 0
	if payload.Extracted.TranscriptCharacters != nil {
		transcriptChars = *payload.Extracted.TranscriptCharacters
	}

	return transcriptSource == "unavailable" && transcriptionProvider == "" && transcriptChars == 0
}

func MaybeTranscribeYouTubeAudioFallback(ctx context.Context, cfg config.Config, source model.SourceDocument, extract model.ExtractResult, opts Options) (model.ExtractResult, bool, error) {
	if source.SourceType != "youtube" {
		return model.ExtractResult{}, false, nil
	}
	if !shouldUseAudioTranscriptionFallback(extract) {
		return model.ExtractResult{}, false, nil
	}

	fallback, err := transcribeYouTubeAudioFallback(ctx, cfg, source, extract, opts)
	if err != nil {
		return model.ExtractResult{}, false, err
	}
	return fallback, true, nil
}

func transcribeYouTubeAudioFallback(ctx context.Context, cfg config.Config, source model.SourceDocument, extract model.ExtractResult, opts Options) (model.ExtractResult, error) {
	if strings.TrimSpace(opts.WhisperModelPath) == "" {
		opts.WhisperModelPath = defaultWhisperModelPath()
	}
	if strings.TrimSpace(opts.MacWhisperBinary) == "" {
		opts.MacWhisperBinary = "mw"
	}

	timeout := opts.Timeout
	if timeout < 10*time.Minute {
		timeout = 10 * time.Minute
	}
	commandCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	tempDir, err := cfg.MkdirTemp("dbrain-youtube-whisper-*")
	if err != nil {
		return model.ExtractResult{}, fmt.Errorf("create temp dir: %w", err)
	}
	defer func() {
		_ = os.RemoveAll(tempDir)
	}()

	audioTemplate := filepath.Join(tempDir, "audio.%(ext)s")
	downloadArgs := []string{
		"--no-playlist",
		"--cookies-from-browser", firstNonEmpty(strings.TrimSpace(opts.YouTubeCookiesArg), cookiesFromBrowserArg(opts.YouTubeBrowser, opts.YouTubeProfile)),
		"-f", "bestaudio/best",
		"-o", audioTemplate,
		source.CanonicalURL,
	}
	downloadCmd := exec.CommandContext(commandCtx, opts.YTDLPBinary, downloadArgs...)
	var downloadStderr bytes.Buffer
	downloadCmd.Stderr = &downloadStderr
	if err := downloadCmd.Run(); err != nil {
		return model.ExtractResult{}, fmt.Errorf("yt-dlp audio download: %s", strings.TrimSpace(downloadStderr.String()))
	}

	audioPath, err := firstDownloadedAudio(tempDir)
	if err != nil {
		return model.ExtractResult{}, err
	}

	transcript, provider, err := transcribeAudioFile(commandCtx, audioPath, opts)
	if err != nil {
		return model.ExtractResult{}, err
	}

	title := strings.TrimSpace(extract.Title)
	if title == "" || title == "- YouTube" {
		title = strings.TrimSpace(source.Title)
	}
	description := strings.TrimSpace(extract.Description)
	if description == "" {
		description = strings.TrimSpace(source.Description)
	}
	siteName := strings.TrimSpace(extract.SiteName)
	if siteName == "" || siteName == "youtube.com" {
		siteName = "YouTube"
	}

	rawJSONBytes, err := json.Marshal(map[string]any{
		"extracted": map[string]any{
			"url":                   source.CanonicalURL,
			"title":                 title,
			"description":           description,
			"siteName":              siteName,
			"content":               "Transcript:\n" + transcript,
			"transcriptSource":      provider,
			"transcriptionProvider": provider,
			"transcriptCharacters":  len(transcript),
		},
	})
	if err != nil {
		return model.ExtractResult{}, fmt.Errorf("marshal whisper transcript json: %w", err)
	}

	return model.ExtractResult{
		CanonicalURL: source.CanonicalURL,
		FinalURL:     source.CanonicalURL,
		Title:        title,
		Description:  description,
		SiteName:     siteName,
		Content:      "Transcript:\n" + transcript,
		RawJSON:      string(rawJSONBytes),
		Status:       "ok",
		FetchedAt:    time.Now().UTC(),
		Tool:         provider,
		ToolVersion:  "",
	}, nil
}

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

func firstDownloadedAudio(dir string) (string, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return "", fmt.Errorf("read audio dir: %w", err)
	}
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		name := entry.Name()
		if strings.HasSuffix(name, ".part") || strings.HasSuffix(name, ".ytdl") || strings.HasSuffix(name, ".txt") {
			continue
		}
		return filepath.Join(dir, name), nil
	}
	return "", fmt.Errorf("downloaded audio not found")
}

func defaultWhisperModelPath() string {
	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	return filepath.Join(home, ".summarize", "cache", "whisper-cpp", "models", "ggml-base.bin")
}

func cookiesFromBrowserArg(browser, profile string) string {
	browser = strings.TrimSpace(browser)
	if browser == "" {
		browser = "chrome"
	}
	profile = strings.TrimSpace(profile)
	if profile == "" {
		return browser
	}
	return browser + ":" + profile
}
