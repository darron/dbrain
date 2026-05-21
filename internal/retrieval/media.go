package retrieval

import "github.com/darron/dbrain/internal/model"

func MediaRefs(refs []model.ItemMediaRef) []MediaRef {
	if len(refs) == 0 {
		return nil
	}
	out := make([]MediaRef, 0, len(refs))
	for _, ref := range refs {
		out = append(out, MediaRef{
			MediaAssetID:   ref.MediaAssetID,
			Ordinal:        ref.Ordinal,
			ExpandedURL:    ref.ExpandedURL,
			RemoteURL:      ref.RemoteURL,
			MediaType:      ref.MediaType,
			DownloadStatus: ref.DownloadStatus,
			ArchiveURL:     ref.ArchiveURL,
			ArchiveStatus:  ref.ArchiveStatus,
			Width:          ref.Width,
			Height:         ref.Height,
		})
	}
	return out
}
