package syncjob

import (
	"strings"
	"time"

	"github.com/darron/dbrain/internal/itemcategorize"
	"github.com/darron/dbrain/internal/linkextract"
	"github.com/darron/dbrain/internal/xapi"
)

func mergeXStats(dst *xapi.Stats, src xapi.Stats) {
	if dst == nil {
		return
	}
	dst.Candidates += src.Candidates
	dst.Requested += src.Requested
	dst.Hydrated += src.Hydrated
	dst.Missing += src.Missing
	dst.APIErrors += src.APIErrors
	dst.Rendered += src.Rendered
	dst.Unchanged += src.Unchanged
	dst.MediaCandidates += src.MediaCandidates
	dst.MediaRequested += src.MediaRequested
	dst.MediaDownloaded += src.MediaDownloaded
	dst.MediaGone += src.MediaGone
	dst.MediaErrors += src.MediaErrors
}

func mergeXBookmarkStage(dst **XBookmarksStage, duration time.Duration, src xapi.BookmarkStats) {
	if *dst == nil {
		*dst = &XBookmarksStage{Duration: duration, Stats: src}
		return
	}
	(*dst).Duration += duration
	mergeXBookmarkStats(&(*dst).Stats, src)
}

func mergeXBookmarkStats(dst *xapi.BookmarkStats, src xapi.BookmarkStats) {
	if dst == nil {
		return
	}
	dst.PagesFetched += src.PagesFetched
	dst.Processed += src.Processed
	dst.Created += src.Created
	dst.Updated += src.Updated
	dst.Unchanged += src.Unchanged
	dst.Rendered += src.Rendered
	dst.StalePages += src.StalePages
	if strings.TrimSpace(src.StoppedReason) != "" {
		dst.StoppedReason = src.StoppedReason
	}
}

func mergeXStage(dst **XStage, duration time.Duration, src xapi.Stats) {
	if *dst == nil {
		*dst = &XStage{Duration: duration, Stats: src}
		return
	}
	(*dst).Duration += duration
	mergeXStats(&(*dst).Stats, src)
}

func mergeLinksStage(dst **LinksStage, duration time.Duration, src linkextract.Stats) {
	if *dst == nil {
		*dst = &LinksStage{Duration: duration, Stats: src}
		return
	}
	(*dst).Duration += duration
	mergeLinkStats(&(*dst).Stats, src)
}

func mergeLinkStats(dst *linkextract.Stats, src linkextract.Stats) {
	if dst == nil {
		return
	}
	dst.ItemsScanned += src.ItemsScanned
	dst.ItemsMarked += src.ItemsMarked
	dst.LinksFound += src.LinksFound
	dst.SourcesCreated += src.SourcesCreated
	dst.LinksCreated += src.LinksCreated
	dst.SourcesQueued += src.SourcesQueued
	dst.SourcesExtracted += src.SourcesExtracted
	dst.SourcesSummarized += src.SourcesSummarized
	dst.SourcesRendered += src.SourcesRendered
	dst.SourcesUnchanged += src.SourcesUnchanged
	dst.Errors += src.Errors
}

func mergeCategorizeStats(values ...itemcategorize.Stats) itemcategorize.Stats {
	var merged itemcategorize.Stats
	for _, value := range values {
		merged.Queued += value.Queued
		merged.Succeeded += value.Succeeded
		merged.Applied += value.Applied
		merged.Skipped += value.Skipped
		merged.Errors += value.Errors
	}
	return merged
}

func shouldSettleXFrontier(opts Options) bool {
	return !opts.Force &&
		opts.XBookmarksEnabled &&
		opts.XEnabled &&
		opts.LinksEnabled
}
