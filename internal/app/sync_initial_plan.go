package app

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/mattn/go-isatty"
	"github.com/spf13/cobra"

	"github.com/darron/dbrain/internal/store"
	"github.com/darron/dbrain/internal/syncjob"
)

type initialSyncPlan struct {
	Enabled   []string
	Skipped   []string
	Unbounded []string
}

func confirmInitialSyncPlanIfNeeded(ctx context.Context, cmd *cobra.Command, st *store.Store, opts syncjob.Options, jsonOut bool) (bool, error) {
	if jsonOut {
		return true, nil
	}
	empty, err := syncCorpusIsEmpty(ctx, st)
	if err != nil {
		return false, err
	}
	if !empty {
		return true, nil
	}

	plan := buildInitialSyncPlan(opts)
	if len(plan.Enabled) == 0 && len(plan.Skipped) == 0 {
		return true, nil
	}

	out := cmd.ErrOrStderr()
	_, _ = fmt.Fprint(out, formatInitialSyncPlan(plan))
	if len(plan.Unbounded) == 0 {
		return true, nil
	}
	if !commandInputIsTerminal(cmd) {
		_, _ = fmt.Fprintln(out, "Non-interactive run: proceeding with the unbounded initial import plan.")
		return true, nil
	}

	ok, err := confirmInitialSyncPlan(cmd.InOrStdin(), out)
	if err != nil {
		return false, err
	}
	if !ok {
		_, _ = fmt.Fprintln(out, "Sync cancelled.")
		return false, nil
	}
	return true, nil
}

func syncCorpusIsEmpty(ctx context.Context, st *store.Store) (bool, error) {
	itemBuckets, err := st.CountItems(ctx, "", "none")
	if err != nil {
		return false, err
	}
	sourceBuckets, err := st.CountSources(ctx, store.SourceCountFilter{}, "none")
	if err != nil {
		return false, err
	}
	return singleCount(itemBuckets) == 0 && singleCount(sourceBuckets) == 0, nil
}

func singleCount(buckets []store.CountBucket) int {
	if len(buckets) == 0 {
		return 0
	}
	return buckets[0].Count
}

func buildInitialSyncPlan(opts syncjob.Options) initialSyncPlan {
	var plan initialSyncPlan
	addStage := func(enabled bool, enabledLabel string, skippedLabel string) {
		if enabled {
			plan.Enabled = append(plan.Enabled, enabledLabel)
		} else {
			plan.Skipped = append(plan.Skipped, skippedLabel)
		}
	}
	if opts.XBookmarksEnabled {
		if opts.XBookmarksLimit <= 0 {
			plan.Unbounded = append(plan.Unbounded, "X bookmarks")
			plan.Enabled = append(plan.Enabled, "X bookmarks=all")
		} else {
			plan.Enabled = append(plan.Enabled, fmt.Sprintf("X bookmarks=%d", opts.XBookmarksLimit))
		}
	} else {
		plan.Skipped = append(plan.Skipped, "X bookmarks")
	}
	addStage(opts.XEnabled, fmt.Sprintf("X hydration=%d", effectiveLimit(opts.XLimit, 100)), "X hydration")
	addStage(opts.XMediaEnabled, fmt.Sprintf("X media=%d", effectiveLimit(opts.XMediaLimit, effectiveLimit(opts.XLimit, 100))), "X media")
	addStage(opts.XPhotoOCREnabled, fmt.Sprintf("X photo OCR=%d", effectiveLimit(opts.XPhotoOCRLimit, effectiveLimit(opts.XLimit, 100))), "X photo OCR")
	addStage(opts.LinksEnabled, fmt.Sprintf("links discover=%d enrich=%d", effectiveLimit(opts.LinkDiscoverLimit, 500), effectiveLimit(opts.LinkLimit, 100)), "links")
	if opts.GitHubEnabled {
		if opts.GitHubLimit <= 0 {
			plan.Unbounded = append(plan.Unbounded, "GitHub stars")
			plan.Enabled = append(plan.Enabled, "GitHub stars=all")
		} else {
			plan.Enabled = append(plan.Enabled, fmt.Sprintf("GitHub stars=%d", opts.GitHubLimit))
		}
	} else {
		plan.Skipped = append(plan.Skipped, "GitHub stars")
	}
	addStage(opts.YouTubeEnabled, fmt.Sprintf("YouTube=%d", effectiveLimit(opts.YouTubeLimit, 50)), "YouTube")
	addStage(opts.FeedsEnabled, fmt.Sprintf("feeds=%d", effectiveLimit(opts.FeedLimit, 100)), "feeds")
	addStage(opts.SourcesEnabled, fmt.Sprintf("sources=%d", effectiveLimit(opts.SourceLimit, 100)), "sources")
	if opts.CategorizeEnabled {
		if opts.CategorizeLimit <= 0 {
			plan.Enabled = append(plan.Enabled, "categorize=all queued")
		} else {
			plan.Enabled = append(plan.Enabled, fmt.Sprintf("categorize=%d per type", opts.CategorizeLimit))
		}
	} else {
		plan.Skipped = append(plan.Skipped, "categorize")
	}
	addStage(opts.AppleNotesEnabled, fmt.Sprintf("Apple Notes=%s", limitOrAll(opts.AppleNotesLimit)), "Apple Notes")
	if opts.AppleNotesEnabled && opts.AppleNotesLimit <= 0 {
		plan.Unbounded = append(plan.Unbounded, "Apple Notes")
	}
	addStage(opts.SafariTabsEnabled, fmt.Sprintf("Safari tabs=%s", limitOrAll(opts.SafariTabsLimit)), "Safari tabs")
	if opts.SafariTabsEnabled && opts.SafariTabsLimit <= 0 {
		plan.Unbounded = append(plan.Unbounded, "Safari tabs")
	}
	addStage(opts.ArchiveMediaEnabled, fmt.Sprintf("media archive=%d", effectiveLimit(opts.ArchiveMediaLimit, 5000)), "media archive")
	addStage(opts.OKFExportEnabled, "OKF export", "OKF export")
	return plan
}

func effectiveLimit(value int, fallback int) int {
	if value > 0 {
		return value
	}
	return fallback
}

func limitOrAll(limit int) string {
	if limit > 0 {
		return fmt.Sprintf("%d", limit)
	}
	return "all"
}

func formatInitialSyncPlan(plan initialSyncPlan) string {
	var b strings.Builder
	b.WriteString("First sync on an empty brain.\n")
	if len(plan.Unbounded) > 0 {
		b.WriteString("About to pull all configured unbounded imports: ")
		b.WriteString(strings.Join(plan.Unbounded, ", "))
		b.WriteString(".\n")
	}
	if len(plan.Enabled) > 0 {
		b.WriteString("Enabled stages: ")
		b.WriteString(strings.Join(plan.Enabled, "; "))
		b.WriteString(".\n")
	}
	if len(plan.Skipped) > 0 {
		b.WriteString("Skipped stages: ")
		b.WriteString(strings.Join(plan.Skipped, "; "))
		b.WriteString(".\n")
	}
	return b.String()
}

func confirmInitialSyncPlan(in io.Reader, out io.Writer) (bool, error) {
	_, _ = fmt.Fprint(out, "Proceed with this first sync? [y/N]: ")
	scanner := bufio.NewScanner(in)
	if !scanner.Scan() {
		if err := scanner.Err(); err != nil {
			return false, err
		}
		return false, nil
	}
	switch strings.ToLower(strings.TrimSpace(scanner.Text())) {
	case "y", "yes":
		return true, nil
	default:
		return false, nil
	}
}

func commandInputIsTerminal(cmd *cobra.Command) bool {
	file, ok := cmd.InOrStdin().(*os.File)
	return ok && file != nil && isatty.IsTerminal(file.Fd())
}
