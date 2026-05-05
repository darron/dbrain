package projection

import (
	"context"

	"github.com/darron/dbrain/internal/config"
	"github.com/darron/dbrain/internal/model"
	"github.com/darron/dbrain/internal/store"
	"github.com/darron/dbrain/internal/vault"
)

type Renderer struct {
	cfg config.Config
	st  *store.Store
}

func NewRenderer(cfg config.Config, st *store.Store) Renderer {
	return Renderer{cfg: cfg, st: st}
}

func (r Renderer) RefreshItem(ctx context.Context, lookup string) (model.Item, error) {
	item, err := r.st.GetItem(ctx, lookup)
	if err != nil {
		return model.Item{}, err
	}
	if item.NotePath == "" {
		return item, nil
	}
	if err := r.WriteItem(item); err != nil {
		return model.Item{}, err
	}
	return item, nil
}

func (r Renderer) WriteItem(item model.Item) error {
	if item.NotePath == "" {
		return nil
	}
	return vault.WriteItem(r.cfg, item)
}

func (r Renderer) RefreshSourceByID(ctx context.Context, sourceID int64) (model.SourceDocument, error) {
	source, err := r.st.GetSourceByID(ctx, sourceID)
	if err != nil {
		return model.SourceDocument{}, err
	}
	if err := r.WriteSource(ctx, source); err != nil {
		return model.SourceDocument{}, err
	}
	return source, nil
}

func (r Renderer) WriteSource(ctx context.Context, source model.SourceDocument) error {
	if source.NotePath == "" {
		return nil
	}
	backlinks, err := r.st.ListBacklinksForSource(ctx, source.ID)
	if err != nil {
		return err
	}
	return vault.WriteSource(r.cfg, source, backlinks)
}
