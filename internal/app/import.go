package app

import (
	"fmt"
	"time"

	"github.com/spf13/cobra"

	"github.com/darron/dbrain/internal/store"
	"github.com/darron/dbrain/internal/youtubeimport"
)

func newImportYouTubeCommand(root *rootOptions) *cobra.Command {
	var browser string
	var profile string
	var limit int
	var watchLater bool
	var liked bool
	var summarize bool
	var force bool
	var transcriber string
	var model string
	var cliProvider string
	var length string
	var timeout time.Duration
	var jsonOut bool

	cmd := &cobra.Command{
		Use:   "youtube",
		Short: "Import authenticated YouTube feed signals",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			cfg, err := loadConfig(root.root, root.configFile)
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

			stats, err := youtubeimport.Run(cmd.Context(), cfg, st, youtubeimport.Options{
				Browser:     browser,
				Profile:     profile,
				Limit:       limit,
				WatchLater:  watchLater,
				Liked:       liked,
				Summarize:   summarize,
				Force:       force,
				Transcriber: transcriber,
				Model:       model,
				CLI:         cliProvider,
				Length:      length,
				Timeout:     timeout,
				Logger:      newLogger(commandDebugEnabled(cmd), cmd.ErrOrStderr()),
			})
			if err != nil {
				return err
			}

			if jsonOut {
				return writeJSON(cmd.OutOrStdout(), stats)
			}

			_, _ = fmt.Fprintf(cmd.OutOrStdout(), "Feeds processed: %d\n", stats.FeedsProcessed)
			_, _ = fmt.Fprintf(cmd.OutOrStdout(), "Items processed: %d\n", stats.ItemsProcessed)
			_, _ = fmt.Fprintf(cmd.OutOrStdout(), "Items created: %d\n", stats.ItemsCreated)
			_, _ = fmt.Fprintf(cmd.OutOrStdout(), "Items deleted: %d\n", stats.ItemsDeleted)
			_, _ = fmt.Fprintf(cmd.OutOrStdout(), "Items updated: %d\n", stats.ItemsUpdated)
			_, _ = fmt.Fprintf(cmd.OutOrStdout(), "Items unchanged: %d\n", stats.ItemsUnchanged)
			_, _ = fmt.Fprintf(cmd.OutOrStdout(), "Items rendered: %d\n", stats.ItemsRendered)
			_, _ = fmt.Fprintf(cmd.OutOrStdout(), "Items skipped: %d\n", stats.ItemsSkipped)
			_, _ = fmt.Fprintf(cmd.OutOrStdout(), "Sources created: %d\n", stats.SourcesCreated)
			_, _ = fmt.Fprintf(cmd.OutOrStdout(), "Source links created: %d\n", stats.LinksCreated)
			_, _ = fmt.Fprintf(cmd.OutOrStdout(), "Sources queued: %d\n", stats.SourcesQueued)
			_, _ = fmt.Fprintf(cmd.OutOrStdout(), "Sources deleted: %d\n", stats.SourcesDeleted)
			_, _ = fmt.Fprintf(cmd.OutOrStdout(), "Sources extracted: %d\n", stats.SourcesExtracted)
			_, _ = fmt.Fprintf(cmd.OutOrStdout(), "Sources summarized: %d\n", stats.SourcesSummarized)
			_, _ = fmt.Fprintf(cmd.OutOrStdout(), "Sources rendered: %d\n", stats.SourcesRendered)
			_, _ = fmt.Fprintf(cmd.OutOrStdout(), "Source unchanged writes: %d\n", stats.SourcesUnchanged)
			_, _ = fmt.Fprintf(cmd.OutOrStdout(), "Errors: %d\n", stats.Errors)
			return nil
		},
	}

	cmd.Flags().StringVar(&browser, "browser", "chrome", "Preferred browser for cookie lookup")
	cmd.Flags().StringVar(&profile, "profile", "", "Browser profile override; requires --browser")
	cmd.Flags().IntVar(&limit, "limit", 50, "Maximum videos to load from each selected YouTube feed")
	cmd.Flags().BoolVar(&watchLater, "watch-later", false, "Import Watch Later")
	cmd.Flags().BoolVar(&liked, "liked", false, "Import liked videos")
	cmd.Flags().BoolVar(&summarize, "summarize", true, "Run summarize.sh extraction and summarization for canonical video sources")
	cmd.Flags().BoolVar(&force, "force", false, "Reprocess imported signals and linked sources even if they were already seen")
	cmd.Flags().StringVar(&transcriber, "transcriber", "auto", "Audio transcription backend for YouTube when captions are missing")
	cmd.Flags().StringVar(&model, "model", "", "Optional summarize model override")
	cmd.Flags().StringVar(&cliProvider, "cli", defaultCLIProvider, "Summarize CLI provider")
	cmd.Flags().StringVar(&length, "length", "medium", "Summary length for summarize.sh")
	cmd.Flags().DurationVar(&timeout, "timeout", 2*time.Minute, "Timeout for yt-dlp and summarize.sh")
	cmd.Flags().BoolVar(&jsonOut, "json", false, "Print import stats as JSON")

	return cmd
}
