package app

import (
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/spf13/cobra"

	"dbrain/internal/itemcategorize"
	"dbrain/internal/store"
)

func newCategorizeCommand(root *rootOptions) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "categorize",
		Short: "Categorize items with an LLM using all available content",
		RunE:  helpCommand,
	}
	cmd.AddCommand(newCategorizeItemCommand(root), newCategorizeBatchCommand(root), newCategorizeRepairCommand(root), newCategorizeAnalyzeCommand(root))
	return cmd
}

func newCategorizeItemCommand(root *rootOptions) *cobra.Command {
	var lookup string
	var model string
	var apply bool
	var includeImages bool
	var timeout time.Duration
	var jsonOut bool

	cmd := &cobra.Command{
		Use:   "item",
		Short: "Categorize a single item by source_key, external_id, or URL",
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
			defer func() { _ = st.Close() }()

			if strings.TrimSpace(lookup) == "" {
				return fmt.Errorf("--lookup is required")
			}

			item, err := st.GetItem(cmd.Context(), lookup)
			if err != nil {
				return err
			}

			result, err := itemcategorize.Run(cmd.Context(), cfg, st, item, itemcategorize.Options{
				Model:         model,
				Timeout:       timeout,
				Apply:         apply,
				IncludeImages: includeImages,
			})
			if err != nil {
				return err
			}

			if jsonOut {
				return writeJSON(cmd.OutOrStdout(), result)
			}

			printCategorizeResult(cmd, item.SourceKey, result)
			if apply {
				_, _ = fmt.Fprintln(cmd.OutOrStdout(), "Saved as user_tags.")
			}
			return nil
		},
	}

	cmd.Flags().StringVar(&lookup, "lookup", "", "Item to categorize (source_key, external_id, canonical_url)")
	cmd.Flags().StringVar(&model, "model", "", "LLM model (ollama/*, openrouter/*, or auto from DBRAIN_CATEGORIZE_MODEL)")
	cmd.Flags().BoolVar(&apply, "apply", false, "Save categories and tags back to the item's user_tags")
	cmd.Flags().BoolVar(&includeImages, "images", false, "Embed archived photos as base64 for vision-capable models")
	cmd.Flags().DurationVar(&timeout, "timeout", 90*time.Second, "LLM request timeout")
	cmd.Flags().BoolVar(&jsonOut, "json", false, "Print result as JSON")

	return cmd
}

func newCategorizeBatchCommand(root *rootOptions) *cobra.Command {
	var model string
	var apply bool
	var includeImages bool
	var force bool
	var limit int
	var concurrency int
	var timeout time.Duration
	var jsonOut bool

	cmd := &cobra.Command{
		Use:   "batch",
		Short: "Categorize items without existing user_tags (or all with --force)",
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
			defer func() { _ = st.Close() }()

			opts := itemcategorize.Options{
				Model:         model,
				Timeout:       timeout,
				Concurrency:   concurrency,
				Limit:         limit,
				Force:         force,
				Apply:         apply,
				IncludeImages: includeImages,
			}

			if !jsonOut {
				var mu sync.Mutex
				opts.OnResult = func(ir itemcategorize.ItemResult) {
					mu.Lock()
					defer mu.Unlock()
					if ir.Error != "" {
						_, _ = fmt.Fprintf(cmd.OutOrStdout(), "[error] %s: %s\n", ir.Item.SourceKey, ir.Error)
						return
					}
					printCategorizeResult(cmd, ir.Item.SourceKey, ir.Result)
				}
			}

			stats, results, err := itemcategorize.Batch(cmd.Context(), cfg, st, opts)
			if err != nil {
				return err
			}

			if jsonOut {
				return writeJSON(cmd.OutOrStdout(), map[string]any{
					"stats":   stats,
					"results": results,
				})
			}

			_, _ = fmt.Fprintln(cmd.OutOrStdout())
			_, _ = fmt.Fprintf(cmd.OutOrStdout(), "Queued:    %d\n", stats.Queued)
			_, _ = fmt.Fprintf(cmd.OutOrStdout(), "Succeeded: %d\n", stats.Succeeded)
			if apply {
				_, _ = fmt.Fprintf(cmd.OutOrStdout(), "Applied:   %d\n", stats.Applied)
			}
			_, _ = fmt.Fprintf(cmd.OutOrStdout(), "Errors:    %d\n", stats.Errors)
			return nil
		},
	}

	cmd.Flags().StringVar(&model, "model", "", "LLM model (ollama/*, openrouter/*, or auto from DBRAIN_CATEGORIZE_MODEL)")
	cmd.Flags().BoolVar(&apply, "apply", false, "Save categories and tags back to each item's user_tags")
	cmd.Flags().BoolVar(&includeImages, "images", false, "Embed archived photos as base64 for vision-capable models")
	cmd.Flags().BoolVar(&force, "force", false, "Re-categorize items even if they already have user_tags")
	cmd.Flags().IntVar(&limit, "limit", 50, "Maximum number of items to process")
	cmd.Flags().IntVar(&concurrency, "concurrency", 2, "Number of concurrent LLM requests")
	cmd.Flags().DurationVar(&timeout, "timeout", 90*time.Second, "Per-item LLM request timeout")
	cmd.Flags().BoolVar(&jsonOut, "json", false, "Print results as JSON")

	return cmd
}

func printCategorizeResult(cmd *cobra.Command, sourceKey string, r itemcategorize.Result) {
	_, _ = fmt.Fprintf(cmd.OutOrStdout(), "\n[%s] model: %s\n", sourceKey, r.Model)
	if r.PrimaryCategory != "" {
		_, _ = fmt.Fprintf(cmd.OutOrStdout(), "  primary:    %s\n", r.PrimaryCategory)
	}
	if len(r.Categories) > 0 {
		_, _ = fmt.Fprintf(cmd.OutOrStdout(), "  categories: %s\n", strings.Join(r.Categories, ", "))
	}
	if len(r.Tags) > 0 {
		_, _ = fmt.Fprintf(cmd.OutOrStdout(), "  tags:       %s\n", strings.Join(r.Tags, ", "))
	}
}
