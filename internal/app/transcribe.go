package app

import (
	"fmt"
	"time"

	"github.com/spf13/cobra"

	"github.com/darron/dbrain/internal/store"
	"github.com/darron/dbrain/internal/xmediatranscribe"
)

func newTranscribeCommand(root *rootOptions) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "transcribe",
		Short: "Transcribe downloaded local media",
		RunE:  helpCommand,
	}
	cmd.AddCommand(newTranscribeXMediaCommand(root))
	return cmd
}

func newTranscribeXMediaCommand(root *rootOptions) *cobra.Command {
	var limit int
	var force bool
	var concurrency int
	var timeout time.Duration
	var mwBinary string
	var mwModel string
	var transcriber string
	var language string
	var whisperBinary string
	var whisperModel string
	var whisperVADModel string
	var summarize bool
	var summaryModel string
	var summaryCLI string
	var summaryLength string
	var ffprobeBinary string
	var jsonOut bool

	cmd := &cobra.Command{
		Use:   "x-media",
		Short: "Transcribe downloaded X video media with whisper.cpp or MacWhisper",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			cfg, err := loadConfig(root.root, root.configFile)
			if err != nil {
				return err
			}

			st, err := store.OpenWithSemanticCache(cfg.DBPath, cfg.CacheDir)
			if err != nil {
				return err
			}
			defer func() {
				_ = st.Close()
			}()

			stats, err := xmediatranscribe.Run(cmd.Context(), cfg, st, xmediatranscribe.Options{
				Limit:            limit,
				Force:            force,
				Concurrency:      concurrency,
				Timeout:          timeout,
				MacWhisperBinary: mwBinary,
				MacWhisperModel:  mwModel,
				Transcriber:      transcriber,
				Language:         language,
				WhisperBinary:    whisperBinary,
				WhisperModelPath: whisperModel,
				WhisperVADPath:   whisperVADModel,
				Summarize:        summarize,
				SummaryModel:     summaryModel,
				SummaryCLI:       summaryCLI,
				SummaryLength:    summaryLength,
				FFprobeBinary:    ffprobeBinary,
				Logger:           newLogger(commandDebugEnabled(cmd), cmd.ErrOrStderr()),
			})
			if err != nil {
				return err
			}

			if jsonOut {
				return writeJSON(cmd.OutOrStdout(), stats)
			}

			_, _ = fmt.Fprintf(cmd.OutOrStdout(), "Items queued: %d\n", stats.ItemsQueued)
			_, _ = fmt.Fprintf(cmd.OutOrStdout(), "Items processed: %d\n", stats.ItemsProcessed)
			_, _ = fmt.Fprintf(cmd.OutOrStdout(), "Items updated: %d\n", stats.ItemsUpdated)
			_, _ = fmt.Fprintf(cmd.OutOrStdout(), "Items unchanged: %d\n", stats.ItemsUnchanged)
			_, _ = fmt.Fprintf(cmd.OutOrStdout(), "Items skipped: %d\n", stats.ItemsSkipped)
			_, _ = fmt.Fprintf(cmd.OutOrStdout(), "Media candidates: %d\n", stats.MediaCandidates)
			_, _ = fmt.Fprintf(cmd.OutOrStdout(), "Media with audio: %d\n", stats.MediaWithAudio)
			_, _ = fmt.Fprintf(cmd.OutOrStdout(), "Media transcribed: %d\n", stats.MediaTranscribed)
			_, _ = fmt.Fprintf(cmd.OutOrStdout(), "Summary candidates: %d\n", stats.SummaryCandidates)
			_, _ = fmt.Fprintf(cmd.OutOrStdout(), "Items summarized: %d\n", stats.ItemsSummarized)
			_, _ = fmt.Fprintf(cmd.OutOrStdout(), "Summary errors: %d\n", stats.SummaryErrors)
			_, _ = fmt.Fprintf(cmd.OutOrStdout(), "Errors: %d\n", stats.Errors)
			return nil
		},
	}

	cmd.Flags().IntVar(&limit, "limit", 25, "Maximum social media items to inspect for downloaded video media")
	cmd.Flags().BoolVar(&force, "force", false, "Retranscribe items even if they already have social media transcript text")
	cmd.Flags().IntVar(&concurrency, "concurrency", 4, "Number of concurrent social media transcriptions to run")
	cmd.Flags().DurationVar(&timeout, "timeout", 5*time.Minute, "Per-media timeout for ffprobe and speech transcription")
	cmd.Flags().StringVar(&transcriber, "transcriber", "", "Transcription backend: auto, whisper.cpp, or macwhisper[:model]")
	cmd.Flags().StringVar(&language, "language", "", "Spoken language code, or auto for detection")
	cmd.Flags().StringVar(&whisperBinary, "whisper-binary", "", "whisper.cpp CLI binary (default whisper-cli)")
	cmd.Flags().StringVar(&whisperModel, "whisper-model", "", "whisper.cpp GGML model path")
	cmd.Flags().StringVar(&whisperVADModel, "whisper-vad-model", "", "Optional whisper.cpp Silero VAD model path")
	cmd.Flags().StringVar(&mwBinary, "mw-binary", "mw", "MacWhisper CLI binary")
	cmd.Flags().StringVar(&mwModel, "model", "", "Optional MacWhisper model override (compatibility flag)")
	cmd.Flags().BoolVar(&summarize, "summarize", true, "Summarize completed social media transcripts after transcription")
	cmd.Flags().StringVar(&summaryModel, "summary-model", "", "Optional model override for social media transcript summaries")
	cmd.Flags().StringVar(&summaryCLI, "summary-cli", defaultCLIProvider, "Summarize CLI provider for social media transcript summaries")
	cmd.Flags().StringVar(&summaryLength, "summary-length", "medium", "Summary length for social media transcript summaries")
	cmd.Flags().StringVar(&ffprobeBinary, "ffprobe-binary", "ffprobe", "ffprobe binary for audio stream detection")
	cmd.Flags().BoolVar(&jsonOut, "json", false, "Print transcription stats as JSON")

	return cmd
}
