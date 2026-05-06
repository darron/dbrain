package sourceenrich

import "github.com/darron/dbrain/internal/model"

func needsEnrichment(source model.SourceDocument, opts Options, promptVersion string, toolName string, toolVersion string) bool {
	if source.ExtractStatus == "" || source.ExtractStatus == model.SourceExtractStatusError {
		return true
	}
	if !opts.Summarize {
		return false
	}
	if source.ExtractStatus != model.SourceExtractStatusOK && source.ExtractStatus != model.SourceExtractStatusEmpty {
		return false
	}
	if source.SummaryStatus == "" || source.SummaryStatus == model.SourceSummaryStatusError {
		return true
	}
	if source.SummaryContentHash != source.ContentHash {
		return true
	}
	if opts.AcceptCurrentSummary {
		return false
	}
	if promptVersion != "" && source.SummaryPromptVersion != promptVersion {
		return true
	}
	if toolName != "" && source.SummaryTool != toolName {
		return true
	}
	if toolVersion != "" && source.SummaryToolVersion != toolVersion {
		return true
	}
	return false
}

func selectSourceDocuments(ordered []int64, byID map[int64]model.SourceDocument, opts Options, promptVersion string, toolName string, toolVersion string) []model.SourceDocument {
	filtered := make([]model.SourceDocument, 0, len(ordered))
	for _, sourceID := range ordered {
		source, ok := byID[sourceID]
		if !ok {
			continue
		}
		if !opts.Force && !needsEnrichment(source, opts, promptVersion, toolName, toolVersion) {
			continue
		}
		filtered = append(filtered, source)
		if opts.Limit > 0 && len(filtered) >= opts.Limit {
			break
		}
	}
	return filtered
}
