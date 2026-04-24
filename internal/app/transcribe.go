package app

import (
	"fmt"
	"time"

	"github.com/spf13/cobra"

	"dbrain/internal/store"
	"dbrain/internal/xmediatranscribe"
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
	var timeout time.Duration
	var mwBinary string
	var mwModel string
	var ffprobeBinary string
	var jsonOut bool

	cmd := &cobra.Command{
		Use:   "x-media",
		Short: "Transcribe downloaded X video media with MacWhisper",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			cfg, err := loadConfig(root.root)
			if err != nil {
				return err
			}

			st, err := store.Open(cfg.DBPath)
			if err != nil {
				return err
			}
			defer func() {
				_ = st.Close()
			}()

			stats, err := xmediatranscribe.Run(cmd.Context(), cfg, st, xmediatranscribe.Options{
				Limit:            limit,
				Force:            force,
				Timeout:          timeout,
				MacWhisperBinary: mwBinary,
				MacWhisperModel:  mwModel,
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
			_, _ = fmt.Fprintf(cmd.OutOrStdout(), "Errors: %d\n", stats.Errors)
			return nil
		},
	}

	cmd.Flags().IntVar(&limit, "limit", 25, "Maximum X items to inspect for downloaded video media")
	cmd.Flags().BoolVar(&force, "force", false, "Retranscribe items even if they already have X media transcript text")
	cmd.Flags().DurationVar(&timeout, "timeout", 5*time.Minute, "Per-media timeout for ffprobe and MacWhisper")
	cmd.Flags().StringVar(&mwBinary, "mw-binary", "mw", "MacWhisper CLI binary")
	cmd.Flags().StringVar(&mwModel, "model", "", "Optional MacWhisper model override")
	cmd.Flags().StringVar(&ffprobeBinary, "ffprobe-binary", "ffprobe", "ffprobe binary for audio stream detection")
	cmd.Flags().BoolVar(&jsonOut, "json", false, "Print transcription stats as JSON")

	return cmd
}
