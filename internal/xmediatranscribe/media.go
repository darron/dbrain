package xmediatranscribe

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/darron/dbrain/internal/audiotranscribe"
	"github.com/darron/dbrain/internal/config"
	"github.com/darron/dbrain/internal/model"
	"github.com/darron/dbrain/internal/vaultfs"
)

func transcribeItemMedia(ctx context.Context, cfg config.Config, refs []model.ItemMediaRef, opts Options) ([]transcriptBlock, Stats, itemTranscriptOutcome) {
	blocks := make([]transcriptBlock, 0, len(refs))
	stats := Stats{}
	videoIndex := 0
	outcome := itemTranscriptOutcome{}
	var root *vaultfs.Root
	defer func() {
		if root != nil {
			_ = root.Close()
		}
	}()

	for _, ref := range refs {
		if ref.DownloadStatus != "downloaded" || strings.TrimSpace(ref.LocalPath) == "" || !ref.LocalPrunedAt.IsZero() {
			continue
		}
		if ref.MediaType != "video" && ref.MediaType != "animated_gif" {
			continue
		}

		stats.MediaCandidates++
		if root == nil {
			var err error
			root, err = vaultfs.Open(cfg.VaultDir)
			if err != nil {
				stats.Errors++
				debugLog(opts.Logger, "x media vault unavailable", "local_path", ref.LocalPath, "error", err.Error())
				continue
			}
		}
		absolutePath, cleanup, err := materializeVaultMedia(cfg, root, ref.LocalPath)
		if err != nil {
			stats.Errors++
			debugLog(opts.Logger, "x media file missing", "local_path", ref.LocalPath, "error", err.Error())
			continue
		}

		audioCtx, cancel := context.WithTimeout(ctx, opts.Timeout)
		hasAudio, err := fileHasAudio(audioCtx, opts.FFprobeBinary, absolutePath)
		cancel()
		if err != nil {
			cleanup()
			stats.Errors++
			debugLog(opts.Logger, "x media audio probe failed", "local_path", ref.LocalPath, "error", err.Error())
			continue
		}
		if !hasAudio {
			cleanup()
			debugLog(opts.Logger, "x media skipped without audio", "local_path", ref.LocalPath)
			outcome = chooseItemTranscriptOutcome(outcome, itemTranscriptOutcome{Status: model.XMediaTranscriptStatusNoAudio})
			continue
		}

		stats.MediaWithAudio++
		transcribeCtx, cancel := context.WithTimeout(ctx, opts.Timeout)
		result, err := audiotranscribe.Transcribe(transcribeCtx, absolutePath, audiotranscribe.Config{
			Backend:             opts.Transcriber,
			Language:            opts.Language,
			WhisperBinary:       opts.WhisperBinary,
			WhisperModelPath:    opts.WhisperModelPath,
			WhisperVADModelPath: opts.WhisperVADPath,
			MacWhisperBinary:    opts.MacWhisperBinary,
			MacWhisperModel:     opts.MacWhisperModel,
		})
		cancel()
		cleanup()
		if err != nil {
			stats.Errors++
			debugLog(opts.Logger, "x media transcription failed", "local_path", ref.LocalPath, "error", err.Error())
			outcome = chooseItemTranscriptOutcome(outcome, itemTranscriptOutcome{
				Status: classifyTranscriptError(err),
				Error:  err.Error(),
			})
			continue
		}
		if result.NoSpeech || shouldSkipTranscript(result.Text) {
			reason := classifyTranscriptSkip(result.Text)
			debugLog(opts.Logger, "x media transcript rejected", "local_path", ref.LocalPath, "reason", reason)
			outcome = chooseItemTranscriptOutcome(outcome, itemTranscriptOutcome{Status: reason})
			continue
		}

		videoIndex++
		stats.MediaTranscribed++
		blocks = append(blocks, transcriptBlock{
			Heading:     fmt.Sprintf("Video %d", videoIndex),
			RemoteURL:   strings.TrimSpace(ref.RemoteURL),
			ExpandedURL: strings.TrimSpace(ref.ExpandedURL),
			LocalPath:   strings.TrimSpace(ref.LocalPath),
			Text:        result.Text,
			Backend:     result.Backend,
			Model:       result.Model,
			Language:    result.Language,
			VADEnabled:  result.VADEnabled,
		})
	}

	return blocks, stats, outcome
}

func materializeVaultMedia(cfg config.Config, root *vaultfs.Root, localPath string) (string, func(), error) {
	source, err := root.Open(localPath)
	if err != nil {
		return "", nil, err
	}
	defer func() { _ = source.Close() }()
	info, err := source.Stat()
	if err != nil {
		return "", nil, err
	}
	if !info.Mode().IsRegular() {
		return "", nil, fmt.Errorf("vault media %q is not a regular file", localPath)
	}

	ext := filepath.Ext(localPath)
	if ext == "" {
		ext = ".media"
	}
	temp, err := cfg.CreateTemp("x-media-transcribe-*" + ext)
	if err != nil {
		return "", nil, err
	}
	tempPath := temp.Name()
	cleanup := func() { _ = os.Remove(tempPath) }
	if _, err := io.Copy(temp, source); err != nil {
		_ = temp.Close()
		cleanup()
		return "", nil, fmt.Errorf("copy vault media %q: %w", localPath, err)
	}
	if err := temp.Close(); err != nil {
		cleanup()
		return "", nil, fmt.Errorf("close temporary vault media %q: %w", localPath, err)
	}
	return tempPath, cleanup, nil
}
