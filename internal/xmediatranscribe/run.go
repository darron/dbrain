package xmediatranscribe

import (
	"bytes"
	"context"
	"fmt"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"dbrain/internal/config"
	"dbrain/internal/itemhash"
	"dbrain/internal/model"
	"dbrain/internal/runtimeenv"
	"dbrain/internal/store"
	"dbrain/internal/summaryconfig"
	"dbrain/internal/vault"
)

const transcriptArticleTitle = "X Media Transcript"

type Options struct {
	Limit            int
	Force            bool
	Concurrency      int
	Timeout          time.Duration
	MacWhisperBinary string
	MacWhisperModel  string
	FFprobeBinary    string
	Summarize        bool
	SummaryModel     string
	SummaryCLI       string
	SummaryLength    string
	SummaryLanguage  string
	Logger           *slog.Logger
}

type Stats struct {
	ItemsQueued       int `json:"items_queued"`
	ItemsProcessed    int `json:"items_processed"`
	ItemsUpdated      int `json:"items_updated"`
	ItemsUnchanged    int `json:"items_unchanged"`
	ItemsSkipped      int `json:"items_skipped"`
	MediaCandidates   int `json:"media_candidates"`
	MediaWithAudio    int `json:"media_with_audio"`
	MediaTranscribed  int `json:"media_transcribed"`
	SummaryCandidates int `json:"summary_candidates"`
	ItemsSummarized   int `json:"items_summarized"`
	SummaryErrors     int `json:"summary_errors"`
	Errors            int `json:"errors"`
}

type transcriptBlock struct {
	Heading     string
	RemoteURL   string
	ExpandedURL string
	LocalPath   string
	Text        string
}

type itemTranscriptOutcome struct {
	Status string
	Error  string
}

var transcriptNoiseMarkers = map[string]struct{}{
	"[music]":       {},
	"[blank_audio]": {},
}

func Run(ctx context.Context, cfg config.Config, st *store.Store, opts Options) (Stats, error) {
	if opts.Limit <= 0 {
		opts.Limit = 100
	}
	if opts.Concurrency <= 0 {
		opts.Concurrency = 1
	}
	if opts.Timeout <= 0 {
		opts.Timeout = 5 * time.Minute
	}
	if strings.TrimSpace(opts.MacWhisperBinary) == "" {
		opts.MacWhisperBinary = "mw"
	}
	if strings.TrimSpace(opts.FFprobeBinary) == "" {
		opts.FFprobeBinary = "ffprobe"
	}
	if strings.TrimSpace(opts.SummaryLength) == "" {
		opts.SummaryLength = "medium"
	}
	opts.SummaryModel = summaryconfig.Model(cfg.RootDir, opts.SummaryModel)
	if strings.TrimSpace(opts.SummaryLanguage) == "" {
		opts.SummaryLanguage = runtimeenv.FirstNonEmpty(cfg.RootDir, "DBRAIN_SUMMARY_LANGUAGE", "DBRAIN_OUTPUT_LANGUAGE", "SUMMARIZE_LANGUAGE")
	}

	items, err := st.ListItemsForXMediaTranscription(ctx, opts.Limit, opts.Force)
	if err != nil {
		return Stats{}, err
	}

	stats := Stats{ItemsQueued: len(items)}
	debugLog(opts.Logger, "x media transcription candidates loaded", "items", len(items), "limit", opts.Limit, "force", opts.Force)

	for _, item := range items {
		stats.ItemsProcessed++
		debugLog(opts.Logger, "transcribing x media item", "source_key", item.SourceKey, "item_id", item.ID)

		refs, err := st.ListItemMediaRefs(ctx, item.ID)
		if err != nil {
			stats.Errors++
			debugLog(opts.Logger, "x media refs failed", "source_key", item.SourceKey, "item_id", item.ID, "error", err.Error())
			continue
		}
		item.Media = refs

		if shouldSkipItem(item, opts.Force) {
			stats.ItemsSkipped++
			debugLog(opts.Logger, "skipping x media transcription item", "source_key", item.SourceKey, "item_id", item.ID, "reason", "existing article text")
			continue
		}

		blocks, blockStats, outcome := transcribeItemMedia(ctx, cfg, refs, opts)
		stats.MediaCandidates += blockStats.MediaCandidates
		stats.MediaWithAudio += blockStats.MediaWithAudio
		stats.MediaTranscribed += blockStats.MediaTranscribed
		stats.Errors += blockStats.Errors

		if len(blocks) == 0 {
			if outcome.Status != "" {
				if err := st.SaveXMediaTranscriptionState(ctx, item.ID, outcome.Status, outcome.Error, time.Now().UTC()); err != nil {
					stats.Errors++
					debugLog(opts.Logger, "x media transcription state save failed", "source_key", item.SourceKey, "item_id", item.ID, "error", err.Error())
				}
			}
			stats.ItemsSkipped++
			debugLog(opts.Logger, "x media transcription produced no transcript", "source_key", item.SourceKey, "item_id", item.ID)
			continue
		}

		item.ArticleTitle = transcriptArticleTitle
		item.ArticleText = renderTranscriptBlocks(blocks)
		item.ContentHash = itemhash.Compute(item)
		item.UpdatedAt = time.Now().UTC()

		result, err := st.UpsertItem(ctx, item)
		if err != nil {
			stats.Errors++
			debugLog(opts.Logger, "x media transcription save failed", "source_key", item.SourceKey, "item_id", item.ID, "error", err.Error())
			continue
		}
		if result.Status == model.UpsertUnchanged {
			stats.ItemsUnchanged++
		} else {
			stats.ItemsUpdated++
			if err := st.InvalidateItemSummary(ctx, result.ItemID); err != nil {
				stats.Errors++
				debugLog(opts.Logger, "x media summary invalidation failed", "source_key", item.SourceKey, "item_id", item.ID, "error", err.Error())
				continue
			}
			item.SummaryText = ""
			item.SummaryJSON = ""
			item.SummaryStatus = ""
			item.SummaryError = ""
			item.SummaryModel = ""
			item.SummaryPromptVersion = ""
			item.SummaryTool = ""
			item.SummaryToolVersion = ""
			item.SummaryInputHash = ""
			item.SummarizedAt = time.Time{}
		}
		if err := st.SaveXMediaTranscriptionState(ctx, item.ID, "ok", "", time.Now().UTC()); err != nil {
			stats.Errors++
			debugLog(opts.Logger, "x media transcription state save failed", "source_key", item.SourceKey, "item_id", item.ID, "error", err.Error())
			continue
		}

		if err := vault.WriteItem(cfg, item); err != nil {
			stats.Errors++
			debugLog(opts.Logger, "x media transcription note write failed", "source_key", item.SourceKey, "item_id", item.ID, "error", err.Error())
			continue
		}
	}

	if opts.Summarize {
		summaryStats := summarizeTranscriptItems(ctx, cfg, st, opts)
		stats.SummaryCandidates += summaryStats.SummaryCandidates
		stats.ItemsSummarized += summaryStats.ItemsSummarized
		stats.SummaryErrors += summaryStats.SummaryErrors
	}

	return stats, nil
}

func shouldSkipItem(item model.Item, force bool) bool {
	if force {
		return false
	}
	return strings.TrimSpace(item.ArticleText) != "" && strings.TrimSpace(item.ArticleTitle) != transcriptArticleTitle
}

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
			outcome = chooseItemTranscriptOutcome(outcome, itemTranscriptOutcome{Status: "no_audio"})
			continue
		}

		stats.MediaWithAudio++
		transcribeCtx, cancel := context.WithTimeout(ctx, opts.Timeout)
		transcript, err := transcribeWithMacWhisper(transcribeCtx, absolutePath, opts)
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
		if shouldSkipTranscript(transcript) {
			reason := classifyTranscriptSkip(transcript)
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
			Text:        transcript,
		})
	}

	return blocks, stats, outcome
}

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

func shouldSkipTranscript(transcript string) bool {
	body := strings.TrimSpace(transcript)
	if body == "" {
		return true
	}
	if _, ok := transcriptNoiseMarkers[strings.ToLower(body)]; ok {
		return true
	}
	return len(body) <= 40
}

func classifyTranscriptSkip(transcript string) string {
	body := strings.TrimSpace(transcript)
	if body == "" {
		return "empty"
	}
	if _, ok := transcriptNoiseMarkers[strings.ToLower(body)]; ok {
		return "noise"
	}
	return "too_short"
}

func classifyTranscriptError(err error) string {
	if err == nil {
		return ""
	}
	if strings.Contains(strings.ToLower(err.Error()), "transcript empty") {
		return "empty"
	}
	return "error"
}

func chooseItemTranscriptOutcome(current itemTranscriptOutcome, next itemTranscriptOutcome) itemTranscriptOutcome {
	if outcomePriority(next.Status) >= outcomePriority(current.Status) {
		return next
	}
	return current
}

func outcomePriority(status string) int {
	switch status {
	case "error":
		return 5
	case "empty":
		return 4
	case "noise":
		return 3
	case "too_short":
		return 2
	case "no_audio":
		return 1
	default:
		return 0
	}
}

func renderTranscriptBlocks(blocks []transcriptBlock) string {
	var b strings.Builder
	for i, block := range blocks {
		if i > 0 {
			b.WriteString("\n\n")
		}
		b.WriteString("### ")
		b.WriteString(block.Heading)
		b.WriteString("\n\n")
		if block.RemoteURL != "" {
			b.WriteString("Remote URL: ")
			b.WriteString(block.RemoteURL)
			b.WriteString("\n")
		}
		if block.ExpandedURL != "" {
			b.WriteString("Post Media URL: ")
			b.WriteString(block.ExpandedURL)
			b.WriteString("\n")
		}
		if block.LocalPath != "" {
			b.WriteString("Local Path: `")
			b.WriteString(block.LocalPath)
			b.WriteString("`\n")
		}
		b.WriteString("\nTranscript:\n\n")
		b.WriteString(strings.TrimSpace(block.Text))
	}
	return strings.TrimSpace(b.String())
}

func debugLog(logger *slog.Logger, msg string, args ...any) {
	if logger == nil {
		return
	}
	logger.Debug(msg, args...)
}
