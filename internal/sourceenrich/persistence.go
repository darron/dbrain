package sourceenrich

import (
	"context"
	"errors"

	"github.com/darron/dbrain/internal/config"
	"github.com/darron/dbrain/internal/model"
	"github.com/darron/dbrain/internal/store"
	"github.com/darron/dbrain/internal/vault"
)

func renderSourceNote(ctx context.Context, cfg config.Config, st *store.Store, sourceID int64) error {
	source, err := st.GetSourceByID(ctx, sourceID)
	if err != nil {
		return err
	}
	backlinks, err := st.ListBacklinksForSource(ctx, sourceID)
	if err != nil {
		return err
	}
	return vault.WriteSource(cfg, source, backlinks)
}

func persistExtractAndSummaryFromExtract(ctx context.Context, cfg config.Config, st *store.Store, source model.SourceDocument, extract model.ExtractResult, opts Options, extractToolVersion string, summaryToolVersion string) (Stats, error) {
	var stats Stats

	if failure, invalid := rejectExtractFailure(source, extract); invalid {
		if failure.Status == "error" {
			if status, errorText, terminal := classifyTerminalExtractError(source, errors.New(failure.Error)); terminal {
				failure.Status = status
				if errorText != "" {
					failure.Error = errorText
				}
			}
		}
		stats.Errors++
		debugLog(opts.Logger, "source extraction rejected", "source_key", source.SourceKey, "url", source.CanonicalURL, "status", failure.Status, "reason", failure.Error)
		if err := saveSourceFailure(ctx, st, source, failure, opts, extractToolVersion, summaryToolVersion); err != nil {
			return stats, err
		}
		return stats, nil
	}

	contentHash := hashText(extract.Content)
	if changed, err := st.SaveSourceExtraction(ctx, source.ID, extract, contentHash); err != nil {
		return stats, err
	} else if changed {
		stats.SourcesExtracted++
		debugLog(opts.Logger, "source extraction saved", "source_key", source.SourceKey, "url", source.CanonicalURL, "status", extract.Status, "content_chars", len(extract.Content), "tool", extract.Tool)
	} else {
		stats.SourcesUnchanged++
	}

	if !opts.Summarize {
		return stats, nil
	}

	if changed, status, err := summarizeFromExtract(ctx, cfg, st, source, extract, opts, summaryToolVersion); err != nil {
		return stats, err
	} else if changed && status == "ok" {
		stats.SourcesSummarized++
	}

	return stats, nil
}
