package prunedmediarepair

import (
	"context"
	"fmt"
	"log/slog"
	"sort"
	"strings"
	"time"

	"github.com/darron/dbrain/internal/config"
	"github.com/darron/dbrain/internal/mastodonapi"
	"github.com/darron/dbrain/internal/mediadownload"
	"github.com/darron/dbrain/internal/model"
	"github.com/darron/dbrain/internal/safehttp"
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
	return runWithDownloaderAndMastodonPolicy(ctx, cfg, st, opts, download, nil)
}

func runWithDownloaderAndMastodonPolicy(ctx context.Context, cfg config.Config, st *store.Store, opts Options, download downloadItemFunc, mastodonPolicyBase *safehttp.Policy) (Stats, error) {
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

	ocrItems := make(map[int64]struct{}, stats.OCRCandidates)
	transcriptItems := make(map[int64]struct{}, stats.TranscriptCandidates)
	unique := make(map[int64]struct{}, stats.OCRCandidates+stats.TranscriptCandidates)
	for _, id := range candidates.OCRItemIDs {
		ocrItems[id] = struct{}{}
		unique[id] = struct{}{}
	}
	for _, id := range candidates.TranscriptItemIDs {
		transcriptItems[id] = struct{}{}
		unique[id] = struct{}{}
	}
	itemIDs := make([]int64, 0, len(unique))
	for id := range unique {
		itemIDs = append(itemIDs, id)
	}
	sort.Slice(itemIDs, func(i, j int) bool { return itemIDs[i] < itemIDs[j] })

	seenAssets := make(map[int64]struct{})
	for _, itemID := range itemIDs {
		refs, err := st.ListItemMediaRefs(ctx, itemID)
		if err != nil {
			return stats, fmt.Errorf("list pruned media refs for item %d: %w", itemID, err)
		}
		_, selectedForOCR := ocrItems[itemID]
		_, selectedForTranscript := transcriptItems[itemID]
		selectedRefs := make([]model.ItemMediaRef, 0, len(refs))
		for _, ref := range refs {
			if !eligiblePrunedArchivedRef(ref, selectedForOCR, selectedForTranscript) {
				continue
			}
			if _, seen := seenAssets[ref.MediaAssetID]; seen {
				continue
			}
			seenAssets[ref.MediaAssetID] = struct{}{}
			selectedRefs = append(selectedRefs, ref)
		}
		if len(selectedRefs) == 0 {
			continue
		}

		item, err := st.GetItemByID(ctx, itemID)
		if err != nil {
			return stats, fmt.Errorf("load pruned media item %d: %w", itemID, err)
		}
		namespace := mediadownload.MediaNamespaceForSourceType(item.SourceType)
		stats.ItemsVisited++
		itemDownloaded := 0
		if namespace == "mastodon" {
			for _, ref := range selectedRefs {
				policy, err := mastodonapi.MediaHTTPPolicy(ref.RemoteURL, mastodonPolicyBase)
				if err != nil {
					changed, saveErr := st.SaveMediaDownload(ctx, ref.MediaAssetID, model.MediaDownloadResult{
						Status: model.MediaDownloadStatusBlocked, Error: err.Error(), AttemptedAt: time.Now().UTC(),
					})
					stats.MediaCandidates++
					stats.MediaRequested++
					stats.MediaBlocked++
					if changed {
						stats.MediaChanged++
					}
					if saveErr != nil {
						return stats, fmt.Errorf("block unsafe Mastodon pruned media for item %d asset %d: %w", itemID, ref.MediaAssetID, saveErr)
					}
					continue
				}
				mediaStats, err := download(ctx, cfg, st, itemID, mediadownload.Options{
					Force: true, AllowedAssetIDs: []int64{ref.MediaAssetID}, MediaNamespace: namespace,
					Timeout: opts.Timeout, Logger: opts.Logger, HTTPPolicy: &policy,
				})
				addMediaDownloadStats(&stats, mediaStats)
				itemDownloaded += mediaStats.Downloaded
				if err != nil {
					return stats, fmt.Errorf("restore pruned media for item %d: %w", itemID, err)
				}
			}
		} else {
			assetIDs := make([]int64, 0, len(selectedRefs))
			for _, ref := range selectedRefs {
				assetIDs = append(assetIDs, ref.MediaAssetID)
			}
			mediaStats, err := download(ctx, cfg, st, itemID, mediadownload.Options{
				Force: true, AllowedAssetIDs: assetIDs, MediaNamespace: namespace, Timeout: opts.Timeout, Logger: opts.Logger,
			})
			addMediaDownloadStats(&stats, mediaStats)
			itemDownloaded += mediaStats.Downloaded
			if err != nil {
				return stats, fmt.Errorf("restore pruned media for item %d: %w", itemID, err)
			}
		}
		if itemDownloaded > 0 {
			stats.ItemsRestored++
		}
	}
	return stats, nil
}

func addMediaDownloadStats(stats *Stats, mediaStats mediadownload.Stats) {
	stats.MediaCandidates += mediaStats.Candidates
	stats.MediaRequested += mediaStats.Requested
	stats.MediaDownloaded += mediaStats.Downloaded
	stats.MediaGone += mediaStats.Gone
	stats.MediaErrors += mediaStats.Errors
	stats.MediaBlocked += mediaStats.Blocked
	stats.MediaChanged += mediaStats.Changed
}

func eligiblePrunedArchivedRef(ref model.ItemMediaRef, selectedForOCR, selectedForTranscript bool) bool {
	if ref.DownloadStatus != model.MediaDownloadStatusDownloaded ||
		strings.TrimSpace(ref.LocalPath) == "" ||
		ref.LocalPrunedAt.IsZero() ||
		ref.ArchiveStatus != model.MediaArchiveStatusArchived {
		return false
	}
	return (selectedForOCR && ref.MediaType == "photo") ||
		(selectedForTranscript && (ref.MediaType == "video" || ref.MediaType == "animated_gif"))
}
