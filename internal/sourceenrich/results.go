package sourceenrich

import (
	"github.com/darron/dbrain/internal/llmprovider"
	"github.com/darron/dbrain/internal/model"
)

func sourceSummaryResult(rootDir string, source model.SourceDocument, summary model.SummaryResult, changed bool) SourceResult {
	result := SourceResult{
		SourceID:           source.ID,
		SourceKey:          source.SourceKey,
		SummaryCreated:     changed && summary.Status == model.SourceSummaryStatusOK,
		SummaryStatus:      summary.Status,
		SummaryModel:       summary.Model,
		SummaryTool:        summary.Tool,
		SummaryToolVersion: summary.ToolVersion,
		SummaryChars:       len(summary.Text),
	}
	if summary.Status != model.SourceSummaryStatusOK {
		result.Error = summary.Error
	}
	provider, apiModel, transport := summaryProviderFields(rootDir, summary.Model)
	result.SummaryProvider = provider
	result.SummaryAPIModel = apiModel
	result.SummaryTransport = transport
	return result
}

func summaryProviderFields(rootDir string, modelName string) (string, string, string) {
	reg, err := llmprovider.RegistryForRoot(rootDir)
	if err != nil {
		return "", "", ""
	}
	ref := reg.ParseModelRef(modelName)
	if !ref.ProviderQualified || ref.Spec == nil {
		return "", "", ""
	}
	return string(ref.Provider), ref.APIModel, string(ref.Spec.Transport)
}

func mergeSourceResult(base SourceResult, update SourceResult) SourceResult {
	if update.SourceID != 0 {
		base.SourceID = update.SourceID
	}
	if update.SourceKey != "" {
		base.SourceKey = update.SourceKey
	}
	if update.Duration != 0 {
		base.Duration = update.Duration
	}
	if update.Error != "" {
		base.Error = update.Error
	}
	if update.Extracted {
		base.Extracted = true
	}
	if update.SummaryCreated {
		base.SummaryCreated = true
	}
	if update.SummaryStatus != "" {
		base.SummaryStatus = update.SummaryStatus
	}
	if update.SummaryModel != "" {
		base.SummaryModel = update.SummaryModel
	}
	if update.SummaryProvider != "" {
		base.SummaryProvider = update.SummaryProvider
	}
	if update.SummaryAPIModel != "" {
		base.SummaryAPIModel = update.SummaryAPIModel
	}
	if update.SummaryTransport != "" {
		base.SummaryTransport = update.SummaryTransport
	}
	if update.SummaryTool != "" {
		base.SummaryTool = update.SummaryTool
	}
	if update.SummaryToolVersion != "" {
		base.SummaryToolVersion = update.SummaryToolVersion
	}
	if update.SummaryChars != 0 {
		base.SummaryChars = update.SummaryChars
	}
	return base
}
