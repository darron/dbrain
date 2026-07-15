package app

import (
	"context"
	"fmt"
	"time"

	"github.com/spf13/cobra"

	"github.com/darron/dbrain/internal/config"
	"github.com/darron/dbrain/internal/prunedmediarepair"
	"github.com/darron/dbrain/internal/store"
)

const maxPrunedMediaRepairLimit = 5000

type repairPrunedMediaRunFunc func(context.Context, config.Config, *store.Store, prunedmediarepair.Options) (prunedmediarepair.Stats, error)

func newRepairPrunedMediaCommand(root *rootOptions) *cobra.Command {
	return newRepairPrunedMediaCommandWithRun(root, prunedmediarepair.Run)
}

func newRepairPrunedMediaCommandWithRun(root *rootOptions, run repairPrunedMediaRunFunc) *cobra.Command {
	var apply bool
	var includeOCR bool
	var includeTranscripts bool
	var limit int
	var timeout time.Duration
	var jsonOut bool

	cmd := &cobra.Command{
		Use:   "pruned-media",
		Short: "Restore archived media needed by pending OCR or transcription work",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			if limit <= 0 {
				return fmt.Errorf("limit must be positive")
			}
			if limit > maxPrunedMediaRepairLimit {
				return fmt.Errorf("limit must not exceed %d", maxPrunedMediaRepairLimit)
			}
			if timeout <= 0 {
				return fmt.Errorf("timeout must be positive")
			}
			if !includeOCR && !includeTranscripts {
				includeOCR = true
				includeTranscripts = true
			}

			var (
				cfg config.Config
				st  *store.Store
				err error
			)
			if apply {
				cfg, err = loadConfig(root.root, root.configFile)
				if err != nil {
					return err
				}
				st, err = store.Open(cfg.DBPath)
			} else {
				cfg, _, err = loadAuditConfig(root.root, root.configFile)
				if err != nil {
					return err
				}
				st, err = store.OpenReadOnly(cfg.DBPath)
			}
			if err != nil {
				return err
			}
			defer func() { _ = st.Close() }()

			stats, err := run(cmd.Context(), cfg, st, prunedmediarepair.Options{
				Apply: apply, OCR: includeOCR, Transcripts: includeTranscripts,
				Limit: limit, Timeout: timeout,
				Logger: newLogger(commandDebugEnabled(cmd), cmd.ErrOrStderr()),
			})
			if err != nil {
				return err
			}
			if jsonOut {
				return writeJSON(cmd.OutOrStdout(), stats)
			}
			mode := "dry-run"
			if stats.Apply {
				mode = "apply"
			}
			_, _ = fmt.Fprintf(cmd.OutOrStdout(), "Mode: %s\n", mode)
			_, _ = fmt.Fprintf(cmd.OutOrStdout(), "OCR candidates: %d\n", stats.OCRCandidates)
			_, _ = fmt.Fprintf(cmd.OutOrStdout(), "Transcript candidates: %d\n", stats.TranscriptCandidates)
			_, _ = fmt.Fprintf(cmd.OutOrStdout(), "Items visited: %d\n", stats.ItemsVisited)
			_, _ = fmt.Fprintf(cmd.OutOrStdout(), "Items restored: %d\n", stats.ItemsRestored)
			_, _ = fmt.Fprintf(cmd.OutOrStdout(), "Media candidates: %d\n", stats.MediaCandidates)
			_, _ = fmt.Fprintf(cmd.OutOrStdout(), "Media requested: %d\n", stats.MediaRequested)
			_, _ = fmt.Fprintf(cmd.OutOrStdout(), "Media downloaded: %d\n", stats.MediaDownloaded)
			_, _ = fmt.Fprintf(cmd.OutOrStdout(), "Media gone: %d\n", stats.MediaGone)
			_, _ = fmt.Fprintf(cmd.OutOrStdout(), "Media errors: %d\n", stats.MediaErrors)
			_, _ = fmt.Fprintf(cmd.OutOrStdout(), "Media blocked: %d\n", stats.MediaBlocked)
			_, _ = fmt.Fprintf(cmd.OutOrStdout(), "Media changed: %d\n", stats.MediaChanged)
			return nil
		},
	}
	cmd.Flags().BoolVar(&apply, "apply", false, "Download and restore matching media (default is read-only dry-run)")
	cmd.Flags().BoolVar(&includeOCR, "ocr", false, "Select pending OCR media only")
	cmd.Flags().BoolVar(&includeTranscripts, "transcripts", false, "Select pending transcript media only")
	cmd.Flags().IntVar(&limit, "limit", maxPrunedMediaRepairLimit, "Maximum items to inspect per selected category")
	cmd.Flags().DurationVar(&timeout, "timeout", 45*time.Second, "Per-media download timeout")
	cmd.Flags().BoolVar(&jsonOut, "json", false, "Print repair stats as JSON")
	return cmd
}
