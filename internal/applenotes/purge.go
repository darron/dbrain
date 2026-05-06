package applenotes

import (
	"context"
	"encoding/json"
	"time"

	"github.com/darron/dbrain/internal/config"
	"github.com/darron/dbrain/internal/projection"
	"github.com/darron/dbrain/internal/store"
)

func purgeExcluded(ctx context.Context, cfg config.Config, st *store.Store, doc NoteDocument, reason string) (bool, error) {
	if st == nil {
		return false, nil
	}
	raw, err := json.Marshal(map[string]any{
		"source_type":  sourceType,
		"source_key":   doc.SourceKey,
		"external_id":  doc.ExternalID,
		"purged":       true,
		"purge_reason": reason,
		"purged_at":    time.Now().UTC().Format(time.RFC3339),
	})
	if err != nil {
		return false, err
	}
	purged, err := st.PurgeItemIndexedContent(ctx, doc.SourceKey, string(raw))
	if err != nil || !purged {
		return purged, err
	}
	if _, err := projection.NewRenderer(cfg, st).RefreshItem(ctx, doc.SourceKey); err != nil {
		return false, err
	}
	return true, nil
}
