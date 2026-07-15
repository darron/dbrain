package prunedmediarepair

import (
	"context"
	"fmt"
	"log/slog"
	"sort"
	"time"

	"github.com/darron/dbrain/internal/config"
	"github.com/darron/dbrain/internal/mediadownload"
	"github.com/darron/dbrain/internal/store"
)

type Options struct {
	Apply       bool
	OCR         bool
	Transcripts bool
	Limit       int
	Timeout     time.Duration
	Logger      *slog.Logger
}

type Stats struct {
	Apply                bool `json:"apply"`
	OCRCandidates        int  `json:"ocr_candidates"`
	TranscriptCandidates int  `json:"transcript_candidates"`
	ItemsVisited         int  `json:"items_visited"`
	ItemsRestored        int  `json:"items_restored"`
	MediaCandidates      int  `json:"media_candidates"`
	MediaRequested       int  `json:"media_requested"`
	MediaDownloaded      int  `json:"media_downloaded"`
	MediaGone            int  `json:"media_gone"`
	MediaErrors          int  `json:"media_errors"`
	MediaBlocked         int  `json:"media_blocked"`
	MediaChanged         int  `json:"media_changed"`
}

type downloadItemFunc func(context.Context, config.Config, *store.Store, int64, mediadownload.Options) (mediadownload.Stats, error)

func Run(ctx context.Context, cfg config.Config, st *store.Store, opts Options) (Stats, error) {
	return runWithDownloader(ctx, cfg, st, opts, mediadownload.RunForItem)
}

func runWithDownloader(ctx context.Context, cfg config.Config, st *store.Store, opts Options, download downloadItemFunc) (Stats, error) {
	stats := Stats{Apply: opts.Apply}
	if opts.Limit <= 0 {
		return stats, fmt.Errorf("pruned media repair limit must be positive")
	}
	if opts.Timeout <= 0 {
		return stats, fmt.Errorf("pruned media repair timeout must be positive")
	}

	candidates, err := st.ListPrunedMediaRepairCandidates(ctx, opts.OCR, opts.Transcripts, opts.Limit)
	if err != nil {
		return stats, err
	}
	stats.OCRCandidates = len(candidates.OCRItemIDs)
	stats.TranscriptCandidates = len(candidates.TranscriptItemIDs)
	if !opts.Apply {
		return stats, nil
	}

	unique := make(map[int64]struct{}, stats.OCRCandidates+stats.TranscriptCandidates)
	for _, id := range candidates.OCRItemIDs {
		unique[id] = struct{}{}
	}
	for _, id := range candidates.TranscriptItemIDs {
		unique[id] = struct{}{}
	}
	itemIDs := make([]int64, 0, len(unique))
	for id := range unique {
		itemIDs = append(itemIDs, id)
	}
	sort.Slice(itemIDs, func(i, j int) bool { return itemIDs[i] < itemIDs[j] })

	for _, itemID := range itemIDs {
		mediaStats, err := download(ctx, cfg, st, itemID, mediadownload.Options{Force: true, Timeout: opts.Timeout, Logger: opts.Logger})
		stats.ItemsVisited++
		if mediaStats.Requested > 0 || mediaStats.Changed > 0 {
			stats.ItemsRestored++
		}
		stats.MediaCandidates += mediaStats.Candidates
		stats.MediaRequested += mediaStats.Requested
		stats.MediaDownloaded += mediaStats.Downloaded
		stats.MediaGone += mediaStats.Gone
		stats.MediaErrors += mediaStats.Errors
		stats.MediaBlocked += mediaStats.Blocked
		stats.MediaChanged += mediaStats.Changed
		if err != nil {
			return stats, fmt.Errorf("restore pruned media for item %d: %w", itemID, err)
		}
	}
	return stats, nil
}
