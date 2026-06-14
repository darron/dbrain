package okf

import (
	"strings"

	"github.com/darron/dbrain/internal/model"
	"github.com/darron/dbrain/internal/store"
)

func renderSourceDocument(source sourceDoc, snapshot store.OKFExportSnapshot, opts ExportOptions, pathByConceptID map[string]string, itemConceptByID map[int64]string) (Document, []OmittedLink, error) {
	doc := Document{
		Path:        source.Path,
		Type:        "Source",
		Title:       sourceTitle(source.Source),
		Description: sourceDescription(source.Source),
		Resource:    firstNonEmpty(source.Source.CanonicalURL, "dbrain://"+source.ConceptID),
		Tags:        sourceTags(source.Source),
		Timestamp:   timestampForSource(source.Source),
		Fields: []Field{
			{Name: "dbrain_concept_id", Value: source.ConceptID},
			{Name: "dbrain_kind", Value: "source"},
			{Name: "dbrain_source_key", Value: source.Source.SourceKey},
			{Name: "dbrain_source_type", Value: source.Source.SourceType},
			{Name: "dbrain_note_path", Value: source.Source.NotePath},
			{Name: "normalized_url", Value: source.Source.NormalizedURL},
			{Name: "domain", Value: source.Source.Domain},
			{Name: "site_name", Value: source.Source.SiteName},
			{Name: "summary_model", Value: source.Source.SummaryModel},
			{Name: "summary_prompt_version", Value: source.Source.SummaryPromptVersion},
		},
	}

	var body strings.Builder
	writeSection(&body, "Overview", doc.Description)
	writeSourceDetails(&body, source.Source)
	writeSourceSummary(&body, source.Source)
	writeSourceRaw(&body, source.Source, opts)
	omitted, err := writeSourceBacklinks(&body, source, snapshot, pathByConceptID, itemConceptByID)
	if err != nil {
		return Document{}, nil, err
	}
	writeSourceCitations(&body, source.Source)
	doc.Body = strings.TrimSpace(body.String()) + "\n"
	return doc, omitted, nil
}

func sourceTags(source model.SourceDocument) []string {
	var tags []string
	if value := normalizeTag(source.SourceType); value != "" {
		tags = append(tags, "source/"+value)
	}
	if value := normalizeTag(source.Domain); value != "" {
		tags = append(tags, "domain/"+value)
	}
	for _, tag := range splitTags(source.UserTags) {
		if normalized := normalizeTag(tag); normalized != "" {
			tags = append(tags, normalized)
		}
	}
	return sortedUnique(tags)
}

func writeSourceDetails(b *strings.Builder, source model.SourceDocument) {
	b.WriteString("\n# Source\n\n")
	writeBullet(b, "Source key", code(source.SourceKey))
	writeBullet(b, "Source type", code(source.SourceType))
	writeBullet(b, "URL", source.CanonicalURL)
	writeBullet(b, "Normalized URL", source.NormalizedURL)
	writeBullet(b, "Domain", code(source.Domain))
	writeBullet(b, "Site", source.SiteName)
}

func writeSourceSummary(b *strings.Builder, source model.SourceDocument) {
	if text := strings.TrimSpace(source.SummaryText); text != "" {
		writeSection(b, "Derived Summary", text)
		return
	}
	if strings.TrimSpace(source.SummaryStatus) == "blocked" || strings.TrimSpace(source.SummaryError) != "" {
		var status strings.Builder
		writeBullet(&status, "Summary status", code(source.SummaryStatus))
		writeBullet(&status, "Summary error", source.SummaryError)
		writeSection(b, "Derived Summary", status.String())
	}
}

func writeSourceRaw(b *strings.Builder, source model.SourceDocument, opts ExportOptions) {
	if !opts.IncludeRaw {
		return
	}
	text := truncateRaw(source.ExtractedText, opts.MaxRawChars)
	if text == "" {
		if strings.TrimSpace(source.ExtractStatus) == "blocked" || strings.TrimSpace(source.ExtractError) != "" {
			var status strings.Builder
			writeBullet(&status, "Extract status", code(source.ExtractStatus))
			writeBullet(&status, "Extract error", source.ExtractError)
			writeSection(b, "Extracted Text", status.String())
		}
		return
	}
	writeSection(b, "Extracted Text", text)
}

func writeSourceBacklinks(b *strings.Builder, source sourceDoc, snapshot store.OKFExportSnapshot, pathByConceptID map[string]string, itemConceptByID map[int64]string) ([]OmittedLink, error) {
	var body strings.Builder
	var omitted []OmittedLink
	for _, ref := range snapshot.SourceBacklinks[source.Source.ID] {
		conceptID := itemConceptByID[ref.ItemID]
		targetPath := pathByConceptID[conceptID]
		if targetPath == "" {
			omitted = append(omitted, omittedByFilter(source.Path, ref.NotePath, conceptID))
			continue
		}
		rel, err := RelativeLink(source.Path, targetPath)
		if err != nil {
			return nil, err
		}
		body.WriteString("- ")
		body.WriteString(MarkdownLink(firstNonEmpty(ref.Title, ref.CanonicalURL, ref.SourceKey), rel))
		body.WriteString(" - referencing item\n")
	}
	writeSection(b, "Referenced By", body.String())
	return omitted, nil
}

func writeSourceCitations(b *strings.Builder, source model.SourceDocument) {
	if strings.TrimSpace(source.CanonicalURL) == "" {
		return
	}
	writeSection(b, "Citations", "[1] "+MarkdownLink("Canonical source", source.CanonicalURL))
}
