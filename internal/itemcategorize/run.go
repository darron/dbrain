package itemcategorize

import (
	"context"
	"fmt"

	"github.com/darron/dbrain/internal/config"
	"github.com/darron/dbrain/internal/model"
	"github.com/darron/dbrain/internal/projection"
	"github.com/darron/dbrain/internal/store"
)

// Run categorizes a single item and optionally saves the result.
func Run(ctx context.Context, cfg config.Config, st *store.Store, item model.Item, opts Options) (Result, error) {
	if err := resolveOpts(ctx, cfg, &opts); err != nil {
		return Result{}, err
	}

	refs, _ := st.ListItemMediaRefs(ctx, item.ID)
	sources, err := st.ListSourceDocumentsForItem(ctx, item.ID)
	if err != nil {
		return Result{}, fmt.Errorf("list linked sources: %w", err)
	}

	s3client := buildS3Client(opts)
	bundle := buildContentBundleWithSources(item, sources)
	photoData := loadPhotoBytes(ctx, cfg, refs, s3client, opts.IncludeImages)

	result, err := callLLM(ctx, bundle, photoData, opts)
	if err != nil {
		return Result{}, err
	}

	if opts.Apply {
		tags := MergeUserTags(item.UserTags, result)
		if err := st.SaveItemUserTags(ctx, item.ID, tags); err != nil {
			return result, fmt.Errorf("save user_tags: %w", err)
		}
		if _, err := projection.NewRenderer(cfg, st).RefreshItem(ctx, item.SourceKey); err != nil {
			return result, fmt.Errorf("refresh item note: %w", err)
		}
	}

	return result, nil
}

// RunSource categorizes a single source and optionally saves the result.
func RunSource(ctx context.Context, cfg config.Config, st *store.Store, source model.SourceDocument, opts Options) (Result, error) {
	if err := resolveOpts(ctx, cfg, &opts); err != nil {
		return Result{}, err
	}

	result, err := callLLM(ctx, buildSourceContentBundle(source), nil, opts)
	if err != nil {
		return Result{}, err
	}

	if opts.Apply {
		tags := MergeUserTags(source.UserTags, result)
		if err := st.SaveSourceUserTags(ctx, source.ID, tags); err != nil {
			return result, fmt.Errorf("save source user_tags: %w", err)
		}
		if _, err := projection.NewRenderer(cfg, st).RefreshSourceByID(ctx, source.ID); err != nil {
			return result, fmt.Errorf("refresh source note: %w", err)
		}
	}

	return result, nil
}
