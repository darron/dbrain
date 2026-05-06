package githubimport

import (
	"context"
	"strings"

	"github.com/darron/dbrain/internal/config"
	"github.com/darron/dbrain/internal/linkextract"
	"github.com/darron/dbrain/internal/model"
	"github.com/darron/dbrain/internal/projection"
	"github.com/darron/dbrain/internal/sourceenrich"
	"github.com/darron/dbrain/internal/store"
	"github.com/darron/dbrain/internal/vault"
)

func summarizeGitHubSources(ctx context.Context, cfg config.Config, st *store.Store, sourceIDs []int64, opts Options) (sourceenrich.Stats, error) {
	if len(sourceIDs) == 0 {
		return sourceenrich.Stats{}, nil
	}
	stats, _, err := sourceenrich.RunSourceIDs(ctx, cfg, st, sourceIDs, sourceenrich.Options{
		Limit:                len(sourceIDs),
		Force:                false,
		AcceptCurrentSummary: true,
		Summarize:            opts.Summarize,
		Model:                opts.Model,
		CLI:                  opts.CLI,
		Length:               opts.Length,
		Timeout:              opts.Timeout,
		Logger:               opts.Logger,
		Binary:               opts.Binary,
	})
	return stats, err
}

func summarizeHomepageSources(ctx context.Context, cfg config.Config, st *store.Store, sourceIDs []int64, opts Options) (sourceenrich.Stats, error) {
	if len(sourceIDs) == 0 {
		return sourceenrich.Stats{}, nil
	}
	stats, _, err := sourceenrich.RunSourceIDs(ctx, cfg, st, sourceIDs, sourceenrich.Options{
		Limit:                len(sourceIDs),
		Force:                opts.Force,
		AcceptCurrentSummary: true,
		Summarize:            opts.Summarize,
		Model:                opts.Model,
		CLI:                  opts.CLI,
		Length:               opts.Length,
		Timeout:              opts.Timeout,
		Logger:               opts.Logger,
		Binary:               opts.Binary,
	})
	return stats, err
}

func renderSourceNotes(ctx context.Context, cfg config.Config, st *store.Store, sourceIDs []int64) (int, error) {
	rendered := 0
	renderer := projection.NewRenderer(cfg, st)
	for _, sourceID := range uniqueSorted(sourceIDs) {
		if _, err := renderer.RefreshSourceByID(ctx, sourceID); err != nil {
			return rendered, err
		}
		rendered++
	}
	return rendered, nil
}

func shouldRefreshGitHubSource(source model.SourceDocument, opts Options) bool {
	if opts.Force {
		return true
	}
	if source.SourceType != "github" {
		return true
	}
	if source.ExtractStatus == "" || source.ExtractStatus == "error" {
		return true
	}
	if strings.TrimSpace(source.ExtractedText) == "" {
		return true
	}
	if source.ExtractTool != "github-api" {
		return true
	}
	return false
}

func homepageSourceCandidate(repo repository) (model.SourceCandidate, bool) {
	homepage := strings.TrimSpace(repo.Homepage)
	if homepage == "" {
		return model.SourceCandidate{}, false
	}
	return linkextract.NormalizeCandidate(homepage)
}

func repoSourceCandidate(repo repository) model.SourceCandidate {
	canonical := strings.TrimSpace(repo.HTMLURL)
	return model.SourceCandidate{
		OriginalURL:   canonical,
		CanonicalURL:  canonical,
		NormalizedURL: canonical,
		SourceType:    "github",
		Domain:        "github.com",
		SourceKey:     "src:" + shortHash(canonical),
		NotePath:      vault.SourceNoteRelativePath("github", repoNoteSlug(repo.FullName)),
	}
}
