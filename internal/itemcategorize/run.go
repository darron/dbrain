package itemcategorize

import (
	"context"
	"fmt"

	"github.com/darron/dbrain/internal/config"
	"github.com/darron/dbrain/internal/model"
	"github.com/darron/dbrain/internal/store"
)

// Run categorizes a single item and optionally saves the result.
func Run(ctx context.Context, cfg config.Config, st *store.Store, item model.Item, opts Options) (Result, error) {
	resolveOpts(cfg, &opts)

	refs, _ := st.ListItemMediaRefs(ctx, item.ID)

	s3client := buildS3Client(opts)
	bundle := buildContentBundle(item)
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
	}

	return result, nil
}

// RunSource categorizes a single source and optionally saves the result.
func RunSource(ctx context.Context, cfg config.Config, st *store.Store, source model.SourceDocument, opts Options) (Result, error) {
	resolveOpts(cfg, &opts)

	result, err := callLLM(ctx, buildSourceContentBundle(source), nil, opts)
	if err != nil {
		return Result{}, err
	}

	if opts.Apply {
		tags := MergeUserTags(source.UserTags, result)
		if err := st.SaveSourceUserTags(ctx, source.ID, tags); err != nil {
			return result, fmt.Errorf("save source user_tags: %w", err)
		}
	}

	return result, nil
}
