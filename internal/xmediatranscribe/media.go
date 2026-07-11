package xmediatranscribe

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/darron/dbrain/internal/audiotranscribe"
	"github.com/darron/dbrain/internal/config"
	"github.com/darron/dbrain/internal/model"
)

func transcribeItemMedia(ctx context.Context, cfg config.Config, refs []model.ItemMediaRef, opts Options) ([]transcriptBlock, Stats, itemTranscriptOutcome) {
	blocks := make([]transcriptBlock, 0, len(refs))
	stats := Stats{}
	videoIndex := 0
	outcome := itemTranscriptOutcome{}

	for _, ref := range refs {
		if ref.DownloadStatus != "downloaded" || strings.TrimSpace(ref.LocalPath) == "" || !ref.LocalPrunedAt.IsZero() {
			continue
		}
		if ref.MediaType != "video" && ref.MediaType != "animated_gif" {
			continue
		}

		stats.MediaCandidates++
		absolutePath := filepath.Join(cfg.VaultDir, filepath.FromSlash(ref.LocalPath))
		if _, err := os.Stat(absolutePath); err != nil {
			stats.Errors++
			debugLog(opts.Logger, "x media file missing", "local_path", ref.LocalPath, "error", err.Error())
			continue
		}

		audioCtx, cancel := context.WithTimeout(ctx, opts.Timeout)
		hasAudio, err := fileHasAudio(audioCtx, opts.FFprobeBinary, absolutePath)
		cancel()
		if err != nil {
			stats.Errors++
			debugLog(opts.Logger, "x media audio probe failed", "local_path", ref.LocalPath, "error", err.Error())
			continue
		}
		if !hasAudio {
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
