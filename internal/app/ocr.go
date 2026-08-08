package app

import (
	"fmt"
	"time"

	"github.com/spf13/cobra"

	"github.com/darron/dbrain/internal/store"
	"github.com/darron/dbrain/internal/xphotoocr"
)

func newOCRCommand(root *rootOptions) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "ocr",
		Short: "Extract text from downloaded images",
		RunE:  helpCommand,
	}
	cmd.AddCommand(newOCRXPhotosCommand(root))
	return cmd
}

func newOCRXPhotosCommand(root *rootOptions) *cobra.Command {
	var limit int
	var force bool
	var concurrency int
	var timeout time.Duration
	var model string
	var tesseractBinary string
	var jsonOut bool

	cmd := &cobra.Command{
		Use:   "x-photos",
		Short: "OCR downloaded X photo media with Ollama/OpenRouter vision plus Tesseract fallback",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			cfg, err := loadConfig(root.root, root.configFile)
			if err != nil {
				return err
			}
			if err := preflightOCRModel(cmd.Context(), cfg, model); err != nil {
				return err
			}

			st, err := store.OpenWithSemanticCache(cfg.DBPath, cfg.CacheDir)
			if err != nil {
				return err
			}
			defer func() {
				_ = st.Close()
			}()

			stats, err := xphotoocr.Run(cmd.Context(), cfg, st, xphotoocr.Options{
				Limit:           limit,
				Force:           force,
				Concurrency:     concurrency,
				Timeout:         timeout,
				Model:           model,
				TesseractBinary: tesseractBinary,
				Logger:          newLogger(commandDebugEnabled(cmd), cmd.ErrOrStderr()),
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
			_, _ = fmt.Fprintf(cmd.OutOrStdout(), "Photo candidates: %d\n", stats.PhotoCandidates)
			_, _ = fmt.Fprintf(cmd.OutOrStdout(), "Photos OCRed: %d\n", stats.PhotosOCRed)
			_, _ = fmt.Fprintf(cmd.OutOrStdout(), "Hosted attempts: %d\n", stats.HostedAttempts)
			_, _ = fmt.Fprintf(cmd.OutOrStdout(), "Hosted fallbacks: %d\n", stats.HostedFallbacks)
			_, _ = fmt.Fprintf(cmd.OutOrStdout(), "Errors: %d\n", stats.Errors)
			return nil
		},
	}

	cmd.Flags().IntVar(&limit, "limit", 100, "Maximum X or Bluesky items to inspect for downloaded photo media")
	cmd.Flags().BoolVar(&force, "force", false, "Re-run OCR even if item OCR text already exists")
	cmd.Flags().IntVar(&concurrency, "concurrency", 4, "Number of concurrent X or Bluesky photo OCR jobs to run")
	cmd.Flags().DurationVar(&timeout, "timeout", 2*time.Minute, "Per-image timeout for hosted OCR and local fallback")
	cmd.Flags().StringVar(&model, "model", "", "OCR model override; supports ollama/<name> and openrouter/<provider>/<model>")
	cmd.Flags().StringVar(&tesseractBinary, "tesseract-binary", "tesseract", "Tesseract binary for local OCR fallback")
	cmd.Flags().BoolVar(&jsonOut, "json", false, "Print OCR stats as JSON")

	return cmd
}
