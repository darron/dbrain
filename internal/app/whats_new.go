package app

import (
	"fmt"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/darron/dbrain/internal/store"
)

func newWhatsNewCommand(root *rootOptions) *cobra.Command {
	var since string
	var cursorToken string
	var limit int
	var types []string
	var view string
	var jsonOut bool

	cmd := &cobra.Command{
		Use:   "whats-new",
		Short: "Show newly imported, enriched, blocked, or failed local evidence",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			cursor, err := reviewCursorFromFlags(since, cursorToken)
			if err != nil {
				return err
			}
			if _, err := store.NormalizeReviewEventTypes(types); err != nil {
				return err
			}
			if _, err := store.NormalizeReviewEventView(view); err != nil {
				return err
			}

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

			page, err := st.ListReviewEvents(cmd.Context(), store.ReviewEventFilter{
				Cursor: cursor,
				Limit:  clampWhatsNewLimit(limit),
				Types:  types,
				View:   view,
			})
			if err != nil {
				return err
			}
			if jsonOut {
				return writeJSON(cmd.OutOrStdout(), page)
			}
			return writeWhatsNewPage(cmd.OutOrStdout(), page)
		},
	}
	cmd.Flags().StringVar(&since, "since", "", "Lower bound timestamp or duration such as 2026-06-21T15:00:00Z, 24h, or 7d")
	cmd.Flags().StringVar(&cursorToken, "cursor", "", "Continuation cursor returned by a previous whats-new call")
	cmd.Flags().IntVar(&limit, "limit", 50, "Maximum events to return")
	cmd.Flags().StringSliceVar(&types, "types", nil, "Event type groups to include: imports,enrichments,failures,all")
	cmd.Flags().StringVar(&view, "view", store.ReviewEventViewEvents, "Output view: events or entities")
	cmd.Flags().BoolVar(&jsonOut, "json", false, "Print structured JSON")
	return cmd
}

func reviewCursorFromFlags(since string, cursorToken string) (store.ReviewCursor, error) {
	since = strings.TrimSpace(since)
	cursorToken = strings.TrimSpace(cursorToken)
	if (since == "") == (cursorToken == "") {
		return store.ReviewCursor{}, fmt.Errorf("exactly one of --since or --cursor is required")
	}
	if cursorToken != "" {
		return store.DecodeReviewCursor(cursorToken)
	}
	return store.ParseReviewSince(since, time.Now().UTC())
}

func clampWhatsNewLimit(value int) int {
	if value < 1 {
		return 1
	}
	if value > 500 {
		return 500
	}
	return value
}
