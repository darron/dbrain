package xphotoocr

import (
	"context"
	"fmt"
	"strings"

	"github.com/darron/dbrain/internal/model"
	"github.com/darron/dbrain/internal/store"
)

type comparePhotoSample struct {
	result CompareImageResult
	ref    model.ItemMediaRef
}

func collectComparePhotoSamples(ctx context.Context, st *store.Store, limit int, includePruned bool) ([]comparePhotoSample, error) {
	items, err := st.ListItemsForXPhotoOCRAudit(ctx, limit, includePruned)
	if err != nil {
		return nil, err
	}
	samples := make([]comparePhotoSample, 0, limit)
	for _, item := range items {
		refs, err := st.ListItemMediaRefs(ctx, item.ID)
		if err != nil {
			return nil, fmt.Errorf("list media refs for %s: %w", item.SourceKey, err)
		}
		for _, ref := range comparePhotoRefs(refs, includePruned) {
			if len(samples) >= limit {
				return samples, nil
			}
			samples = append(samples, comparePhotoSample{
				ref: ref,
				result: CompareImageResult{
					Index:        len(samples) + 1,
					ItemID:       item.ID,
					SourceKey:    item.SourceKey,
					Title:        item.Title,
					CanonicalURL: item.CanonicalURL,
					NotePath:     item.NotePath,
					PhotoOrdinal: ref.Ordinal,
					LocalPath:    ref.LocalPath,
					RemoteURL:    ref.RemoteURL,
					ExpandedURL:  ref.ExpandedURL,
					ExistingOCR: ExistingOCR{
						Status: item.OCRStatus,
						Model:  item.OCRModel,
						Tool:   item.OCRTool,
						Text:   item.OCRText,
					},
				},
			})
		}
	}
	return samples, nil
}

func comparePhotoRefs(refs []model.ItemMediaRef, includePruned bool) []model.ItemMediaRef {
	photos := make([]model.ItemMediaRef, 0, len(refs))
	for _, ref := range refs {
		if ref.MediaType != "photo" || ref.DownloadStatus != "downloaded" || strings.TrimSpace(ref.LocalPath) == "" {
			continue
		}
		if !includePruned && !ref.LocalPrunedAt.IsZero() {
			continue
		}
		photos = append(photos, ref)
	}
	return photos
}
