package retrieval

import "github.com/darron/dbrain/internal/model"

func RelatedDocumentFromItem(item model.Item) RelatedDocument {
	return RelatedDocument{
		ID:                     item.ID,
		SourceKey:              item.SourceKey,
		SourceType:             item.SourceType,
		ExternalID:             item.ExternalID,
		CanonicalURL:           item.CanonicalURL,
		Title:                  item.Title,
		AuthorHandle:           item.AuthorHandle,
		AuthorName:             item.AuthorName,
		PublishedAt:            item.PublishedAt,
		SavedAt:                item.SavedAt,
		Language:               item.Language,
		PrimaryCategory:        item.PrimaryCategory,
		PrimaryDomain:          item.PrimaryDomain,
		NotePath:               item.NotePath,
		UserTags:               item.UserTags,
		XPostStatus:            item.XPostStatus,
		SummaryStatus:          item.SummaryStatus,
		SummaryModel:           item.SummaryModel,
		SummaryTool:            item.SummaryTool,
		OCRStatus:              item.OCRStatus,
		OCRModel:               item.OCRModel,
		OCRTool:                item.OCRTool,
		XMediaTranscriptStatus: item.XMediaTranscriptStatus,
		ImportedAt:             FormatTime(item.ImportedAt),
		UpdatedAt:              FormatTime(item.UpdatedAt),
		LastSeenAt:             FormatTime(item.LastSeenAt),
	}
}
