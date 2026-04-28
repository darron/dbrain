package mcpserver

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"
	"time"
	"unicode"

	"dbrain/internal/model"
)

const (
	getModeBrief    = "brief"
	getModeEvidence = "evidence"
	getModeRaw      = "raw"
	getModeRendered = "rendered"

	defaultGetSectionChars = 4000
	maxGetSectionCharsHard = 50000
	maxGetRelatedSections  = 5
	maxGetManyLookups      = 20
)

type getSection struct {
	Name      string `json:"name"`
	Role      string `json:"role"`
	Status    string `json:"status,omitempty"`
	Model     string `json:"model,omitempty"`
	Tool      string `json:"tool,omitempty"`
	At        string `json:"at,omitempty"`
	Chars     int    `json:"chars"`
	Text      string `json:"text,omitempty"`
	Truncated bool   `json:"truncated"`
}

type getManyError struct {
	Lookup string `json:"lookup"`
	Error  string `json:"error"`
}

func resolveGetContentMode(value string, includeContent *bool) (string, error) {
	mode := strings.ToLower(strings.TrimSpace(value))
	if mode == "" {
		if includeContent != nil && !*includeContent {
			return getModeBrief, nil
		}
		return getModeEvidence, nil
	}
	switch mode {
	case getModeBrief, getModeEvidence, getModeRaw, getModeRendered:
		return mode, nil
	default:
		return "", fmt.Errorf("unsupported content_mode %q; expected brief, evidence, raw, or rendered", value)
	}
}

func (s *Server) getPayloadForLookup(ctx context.Context, lookup string, mode string, maxChars int, query string) (map[string]interface{}, string, error) {
	lookup = strings.TrimSpace(lookup)
	if lookup == "" {
		return nil, "", fmt.Errorf("lookup is required")
	}
	query = strings.TrimSpace(query)
	if item, err := s.st.GetItem(ctx, lookup); err == nil {
		return s.getItemPayload(ctx, item, mode, maxChars, query)
	}
	source, err := s.st.GetSource(ctx, lookup)
	if err != nil {
		return nil, "", err
	}
	return s.getSourcePayload(ctx, source, mode, maxChars, query)
}

func maxGetSectionChars(value int) int {
	if value <= 0 {
		return defaultGetSectionChars
	}
	if value > maxGetSectionCharsHard {
		return maxGetSectionCharsHard
	}
	return value
}

func (s *Server) getItemPayload(ctx context.Context, item model.Item, mode string, maxChars int, query string) (map[string]interface{}, string, error) {
	note := filepath.Join(s.cfg.VaultDir, filepath.FromSlash(item.NotePath))
	available := itemAvailableSections(item)
	relatedItems, relatedItemSections, err := s.itemRelatedItemSections(ctx, item)
	if err != nil {
		return nil, "", err
	}
	relatedSources, relatedSourceSections, err := s.itemRelatedSourceSections(ctx, item)
	if err != nil {
		return nil, "", err
	}
	available = append(available, relatedItemSections...)
	available = append(available, relatedSourceSections...)

	sections := sectionsForMode(available, mode, maxChars, query)
	if mode == getModeRendered {
		content, err := readNote(note)
		if err != nil {
			return nil, "", err
		}
		sections = []getSection{makeGetSection("rendered_note", "rendered", "", "", "", time.Time{}, content, maxChars)}
	}

	payload := map[string]interface{}{
		"kind":                  "item",
		"title":                 item.Title,
		"source_key":            item.SourceKey,
		"url":                   item.CanonicalURL,
		"note":                  note,
		"note_path":             item.NotePath,
		"content_mode":          mode,
		"max_chars_per_section": maxChars,
		"available_sections":    sectionCatalog(available),
		"content_sections":      sections,
		"item":                  slimItem(item),
	}
	if query != "" {
		payload["query"] = query
	}
	if len(relatedItems) > 0 {
		payload["related_items"] = relatedItems
	}
	if len(relatedSources) > 0 {
		payload["related_sources"] = relatedSources
	}
	if mode == getModeRendered && len(sections) > 0 {
		payload["content"] = sections[0].Text
	}
	return payload, formatGetPayload(payload), nil
}

func (s *Server) getSourcePayload(ctx context.Context, source model.SourceDocument, mode string, maxChars int, query string) (map[string]interface{}, string, error) {
	note := filepath.Join(s.cfg.VaultDir, filepath.FromSlash(source.NotePath))
	available := sourceAvailableSections(source)
	backlinks, backlinkSections, err := s.sourceBacklinkSections(ctx, source)
	if err != nil {
		return nil, "", err
	}
	available = append(available, backlinkSections...)

	sections := sectionsForMode(available, mode, maxChars, query)
	if mode == getModeRendered {
		content, err := readNote(note)
		if err != nil {
			return nil, "", err
		}
		sections = []getSection{makeGetSection("rendered_note", "rendered", "", "", "", time.Time{}, content, maxChars)}
	}

	title := firstNonEmpty(source.Title, source.CanonicalURL)
	payload := map[string]interface{}{
		"kind":                  "source",
		"title":                 title,
		"source_key":            source.SourceKey,
		"url":                   source.CanonicalURL,
		"note":                  note,
		"note_path":             source.NotePath,
		"content_mode":          mode,
		"max_chars_per_section": maxChars,
		"available_sections":    sectionCatalog(available),
		"content_sections":      sections,
		"source":                slimSource(source),
	}
	if query != "" {
		payload["query"] = query
	}
	if len(backlinks) > 0 {
		payload["backlinks"] = backlinks
	}
	if mode == getModeRendered && len(sections) > 0 {
		payload["content"] = sections[0].Text
	}
	return payload, formatGetPayload(payload), nil
}

func (s *Server) itemRelatedItemSections(ctx context.Context, item model.Item) ([]map[string]interface{}, []getSection, error) {
	childIDs, err := s.st.ListItemChildLinks(ctx, item.ID, "quoted_post")
	if err != nil {
		return nil, nil, err
	}
	if len(childIDs) == 0 {
		return nil, nil, nil
	}
	related := make([]map[string]interface{}, 0, min(len(childIDs), maxGetRelatedSections))
	sections := make([]getSection, 0, min(len(childIDs), maxGetRelatedSections))
	for _, childID := range childIDs {
		if len(sections) >= maxGetRelatedSections {
			break
		}
		child, err := s.st.GetItemByID(ctx, childID)
		if err != nil {
			continue
		}
		related = append(related, slimItem(child))
		text := relatedItemText(child)
		if strings.TrimSpace(text) == "" {
			continue
		}
		sections = append(sections, makeGetSection("quoted_post:"+child.SourceKey, "related_item", child.XPostStatus, child.SummaryModel, child.SummaryTool, child.XPostFetchedAt, text, 0))
	}
	return related, sections, nil
}

func (s *Server) itemRelatedSourceSections(ctx context.Context, item model.Item) ([]model.ItemSourceRef, []getSection, error) {
	refs, err := s.st.ListSourcesForItem(ctx, item.ID)
	if err != nil {
		return nil, nil, err
	}
	if len(refs) == 0 {
		return nil, nil, nil
	}
	if len(refs) > maxGetRelatedSections {
		refs = refs[:maxGetRelatedSections]
	}
	sections := make([]getSection, 0, len(refs))
	for _, ref := range refs {
		source, err := s.st.GetSource(ctx, ref.SourceKey)
		if err != nil {
			continue
		}
		text := relatedSourceText(source)
		if strings.TrimSpace(text) == "" {
			continue
		}
		sections = append(sections, makeGetSection("linked_source:"+source.SourceKey, "related_source", firstNonEmpty(source.SummaryStatus, source.ExtractStatus), source.SummaryModel, firstNonEmpty(source.SummaryTool, source.ExtractTool), firstNonZeroTime(source.SummarizedAt, source.ExtractedAt), text, 0))
	}
	return refs, sections, nil
}

func (s *Server) sourceBacklinkSections(ctx context.Context, source model.SourceDocument) ([]model.SourceBacklink, []getSection, error) {
	refs, err := s.st.ListBacklinksForSource(ctx, source.ID)
	if err != nil {
		return nil, nil, err
	}
	if len(refs) == 0 {
		return nil, nil, nil
	}
	if len(refs) > maxGetRelatedSections {
		refs = refs[:maxGetRelatedSections]
	}
	sections := make([]getSection, 0, len(refs))
	for _, ref := range refs {
		item, err := s.st.GetItem(ctx, ref.SourceKey)
		if err != nil {
			continue
		}
		text := relatedItemText(item)
		if strings.TrimSpace(text) == "" {
			continue
		}
		sections = append(sections, makeGetSection("referencing_item:"+item.SourceKey, "related_item", item.XPostStatus, item.SummaryModel, item.SummaryTool, item.XPostFetchedAt, text, 0))
	}
	return refs, sections, nil
}

func relatedItemText(item model.Item) string {
	var b strings.Builder
	b.WriteString("[")
	b.WriteString(item.SourceKey)
	b.WriteString("] ")
	b.WriteString(firstNonEmpty(item.Title, item.CanonicalURL))
	if item.CanonicalURL != "" {
		b.WriteString("\nURL: ")
		b.WriteString(item.CanonicalURL)
	}
	if item.AuthorName != "" || item.AuthorHandle != "" {
		b.WriteString("\nAuthor: ")
		b.WriteString(firstNonEmpty(item.AuthorName, item.AuthorHandle))
		if item.AuthorHandle != "" && item.AuthorName != "" {
			b.WriteString(" (@")
			b.WriteString(strings.TrimPrefix(item.AuthorHandle, "@"))
			b.WriteString(")")
		}
	}
	if strings.TrimSpace(item.UserTags) != "" {
		b.WriteString("\nUser tags: ")
		b.WriteString(strings.TrimSpace(item.UserTags))
	}
	appendDistinctTextBlock(&b, "Post text", firstNonEmpty(item.XPostText, item.Text))
	appendDistinctTextBlock(&b, "Media transcript", itemMediaTranscriptText(item))
	appendDistinctTextBlock(&b, "Image OCR", item.OCRText)
	appendDistinctTextBlock(&b, "Summary", item.SummaryText)
	appendDistinctTextBlock(&b, "Article text", nonTranscriptArticleText(item))
	return b.String()
}

func relatedSourceText(source model.SourceDocument) string {
	var b strings.Builder
	b.WriteString("[")
	b.WriteString(source.SourceKey)
	b.WriteString("] ")
	b.WriteString(firstNonEmpty(source.Title, source.CanonicalURL))
	if source.CanonicalURL != "" {
		b.WriteString("\nURL: ")
		b.WriteString(source.CanonicalURL)
	}
	body := firstNonEmpty(source.SummaryText, source.ExtractedText, source.Description)
	if body != "" {
		b.WriteString("\n\n")
		b.WriteString(body)
	}
	return b.String()
}

func itemAvailableSections(item model.Item) []getSection {
	var sections []getSection
	appendUniqueSection(&sections, makeGetSection("summary_text", "derived", item.SummaryStatus, item.SummaryModel, item.SummaryTool, item.SummarizedAt, item.SummaryText, 0))
	appendUniqueSection(&sections, makeGetSection("x_post_text", "raw", item.XPostStatus, "", "", item.XPostFetchedAt, item.XPostText, 0))
	appendUniqueSection(&sections, makeGetSection("x_media_transcript", "raw_transcript", item.XMediaTranscriptStatus, "", "", item.XMediaTranscriptAt, itemMediaTranscriptText(item), 0))
	appendUniqueSection(&sections, makeGetSection("article_text", "raw", "", "", "", time.Time{}, nonTranscriptArticleText(item), 0))
	appendUniqueSection(&sections, makeGetSection("text", "raw", "", "", "", time.Time{}, item.Text, 0))
	appendUniqueSection(&sections, makeGetSection("ocr_text", "raw_ocr", item.OCRStatus, item.OCRModel, item.OCRTool, item.OCRAt, item.OCRText, 0))
	appendUniqueSection(&sections, makeGetSection("x_post_json", "raw_json", item.XPostStatus, "", "", item.XPostFetchedAt, item.XPostJSON, 0))
	appendUniqueSection(&sections, makeGetSection("ocr_json", "raw_json", item.OCRStatus, item.OCRModel, item.OCRTool, item.OCRAt, item.OCRJSON, 0))
	appendUniqueSection(&sections, makeGetSection("summary_json", "derived_json", item.SummaryStatus, item.SummaryModel, item.SummaryTool, item.SummarizedAt, item.SummaryJSON, 0))
	appendUniqueSection(&sections, makeGetSection("raw_json", "raw_json", "", "", "", time.Time{}, item.RawJSON, 0))
	return sections
}

func itemMediaTranscriptText(item model.Item) string {
	if strings.EqualFold(strings.TrimSpace(item.ArticleTitle), "X Media Transcript") {
		return strings.TrimSpace(item.ArticleText)
	}
	if strings.TrimSpace(item.XMediaTranscriptStatus) == "ok" {
		return strings.TrimSpace(item.ArticleText)
	}
	return ""
}

func nonTranscriptArticleText(item model.Item) string {
	if itemMediaTranscriptText(item) != "" {
		return ""
	}
	return strings.TrimSpace(item.ArticleText)
}

func appendDistinctTextBlock(b *strings.Builder, label string, value string) {
	value = strings.TrimSpace(value)
	if value == "" {
		return
	}
	if strings.Contains(b.String(), value) {
		return
	}
	b.WriteString("\n\n")
	b.WriteString(label)
	b.WriteString(":\n")
	b.WriteString(value)
}

func sourceAvailableSections(source model.SourceDocument) []getSection {
	var sections []getSection
	appendUniqueSection(&sections, makeGetSection("summary_text", "derived", source.SummaryStatus, source.SummaryModel, source.SummaryTool, source.SummarizedAt, source.SummaryText, 0))
	appendUniqueSection(&sections, makeGetSection("extracted_text", "raw", source.ExtractStatus, "", source.ExtractTool, source.ExtractedAt, source.ExtractedText, 0))
	appendUniqueSection(&sections, makeGetSection("description", "metadata", "", "", "", time.Time{}, source.Description, 0))
	appendUniqueSection(&sections, makeGetSection("extract_json", "raw_json", source.ExtractStatus, "", source.ExtractTool, source.ExtractedAt, source.ExtractJSON, 0))
	appendUniqueSection(&sections, makeGetSection("summary_json", "derived_json", source.SummaryStatus, source.SummaryModel, source.SummaryTool, source.SummarizedAt, source.SummaryJSON, 0))
	return sections
}

func sectionsForMode(available []getSection, mode string, maxChars int, query string) []getSection {
	if mode == getModeBrief {
		return nil
	}
	var sections []getSection
	for _, section := range available {
		if mode == getModeEvidence && strings.Contains(section.Role, "json") {
			continue
		}
		if mode == getModeEvidence && section.Name == "raw_json" {
			continue
		}
		if mode == getModeRaw && strings.HasPrefix(section.Role, "derived") {
			continue
		}
		if mode == getModeRaw && strings.HasPrefix(section.Role, "related_") {
			continue
		}
		if mode == getModeEvidence {
			section.Text, section.Truncated = evidenceSectionText(section, maxChars, query)
		} else {
			section.Text, section.Truncated = truncateWithFlag(section.Text, maxChars)
		}
		appendUniqueSection(&sections, section)
	}
	return sections
}

func evidenceSectionText(section getSection, maxChars int, query string) (string, bool) {
	if strings.TrimSpace(query) == "" {
		return truncateWithFlag(section.Text, maxChars)
	}
	text, truncated, matched := queryWindowWithFlag(section.Text, query, maxChars)
	if matched {
		return text, truncated
	}
	return truncateWithFlag(section.Text, maxChars)
}

func makeGetSection(name, role, status, modelName, tool string, at time.Time, text string, maxChars int) getSection {
	trimmed := strings.TrimSpace(text)
	section := getSection{
		Name:  name,
		Role:  role,
		Chars: len([]rune(trimmed)),
	}
	if status = strings.TrimSpace(status); status != "" {
		section.Status = status
	}
	if modelName = strings.TrimSpace(modelName); modelName != "" {
		section.Model = modelName
	}
	if tool = strings.TrimSpace(tool); tool != "" {
		section.Tool = tool
	}
	if !at.IsZero() {
		section.At = at.UTC().Format(time.RFC3339)
	}
	if maxChars > 0 {
		section.Text, section.Truncated = truncateWithFlag(trimmed, maxChars)
	} else {
		section.Text = trimmed
	}
	return section
}

func appendUniqueSection(sections *[]getSection, section getSection) {
	if strings.TrimSpace(section.Text) == "" {
		return
	}
	for _, existing := range *sections {
		if existing.Text == section.Text {
			return
		}
	}
	*sections = append(*sections, section)
}

func sectionCatalog(sections []getSection) []getSection {
	catalog := make([]getSection, 0, len(sections))
	for _, section := range sections {
		section.Text = ""
		section.Truncated = false
		catalog = append(catalog, section)
	}
	return catalog
}

func truncateWithFlag(value string, maxChars int) (string, bool) {
	value = strings.TrimSpace(value)
	if maxChars <= 0 {
		return value, false
	}
	runes := []rune(value)
	if len(runes) <= maxChars {
		return value, false
	}
	return strings.TrimSpace(string(runes[:maxChars])), true
}

func queryWindowWithFlag(value string, query string, maxChars int) (string, bool, bool) {
	text := strings.Join(strings.Fields(strings.TrimSpace(value)), " ")
	if text == "" {
		return "", false, false
	}
	terms := queryWindowTerms(query)
	if len(terms) == 0 {
		return "", false, false
	}
	lowerText := strings.ToLower(text)
	bestByteIndex := -1
	bestRuneIndex := 0
	bestRuneLen := 0
	for _, term := range terms {
		if term == "" {
			continue
		}
		idx := strings.Index(lowerText, term)
		if idx < 0 {
			continue
		}
		runeLen := len([]rune(term))
		if bestByteIndex == -1 || runeLen > bestRuneLen || (runeLen == bestRuneLen && idx < bestByteIndex) {
			bestByteIndex = idx
			bestRuneIndex = len([]rune(lowerText[:idx]))
			bestRuneLen = runeLen
		}
	}
	if bestByteIndex < 0 {
		return "", false, false
	}
	runes := []rune(text)
	if maxChars <= 0 || len(runes) <= maxChars {
		return text, false, true
	}
	matchRune := bestRuneIndex
	matchRuneLen := bestRuneLen
	bodyMax := maxChars - 6
	if bodyMax < 40 {
		bodyMax = maxChars
	}
	if bodyMax <= 0 {
		bodyMax = maxChars
	}
	contextBefore := bodyMax / 3
	start := matchRune - contextBefore
	if start < 0 {
		start = 0
	}
	if start+bodyMax > len(runes) {
		start = len(runes) - bodyMax
		if start < 0 {
			start = 0
		}
	}
	end := start + bodyMax
	if minEnd := matchRune + matchRuneLen; end < minEnd {
		end = minEnd
		if end > len(runes) {
			end = len(runes)
		}
		start = end - bodyMax
		if start < 0 {
			start = 0
		}
	}
	if end > len(runes) {
		end = len(runes)
	}
	snippet := strings.TrimSpace(string(runes[start:end]))
	if start > 0 {
		snippet = "..." + snippet
	}
	if end < len(runes) {
		snippet += "..."
	}
	return snippet, start > 0 || end < len(runes), true
}

func queryWindowTerms(query string) []string {
	query = strings.TrimSpace(strings.ToLower(query))
	if query == "" {
		return nil
	}
	stopwords := map[string]struct{}{
		"a": {}, "about": {}, "an": {}, "and": {}, "are": {}, "brain": {}, "can": {}, "dbrain": {}, "do": {}, "does": {}, "for": {},
		"evidence": {}, "find": {}, "give": {}, "have": {}, "how": {}, "i": {}, "if": {}, "in": {}, "include": {}, "is": {}, "know": {}, "me": {}, "my": {}, "of": {}, "on": {},
		"overview": {}, "present": {}, "related": {}, "saved": {}, "show": {}, "tag": {}, "tags": {}, "tell": {}, "the": {}, "to": {}, "use": {}, "using": {}, "we": {}, "what": {}, "why": {}, "you": {}, "your": {},
	}
	parts := strings.FieldsFunc(query, func(r rune) bool {
		return !unicode.IsLetter(r) && !unicode.IsDigit(r)
	})
	filtered := make([]string, 0, len(parts))
	for _, part := range parts {
		part = strings.TrimSpace(part)
		if len([]rune(part)) < 2 {
			continue
		}
		if _, skip := stopwords[part]; skip {
			continue
		}
		filtered = append(filtered, part)
	}
	candidates := make([]string, 0, len(filtered)+3)
	if len(filtered) > 1 {
		candidates = append(candidates, strings.Join(filtered, " "), strings.Join(filtered, "-"))
	}
	if len([]rune(query)) <= 120 {
		candidates = append(candidates, query)
	}
	candidates = append(candidates, filtered...)

	seen := map[string]struct{}{}
	terms := make([]string, 0, len(candidates))
	for _, term := range candidates {
		term = strings.TrimSpace(term)
		if term == "" {
			continue
		}
		if _, ok := seen[term]; ok {
			continue
		}
		seen[term] = struct{}{}
		terms = append(terms, term)
	}
	return terms
}

func slimItem(item model.Item) map[string]interface{} {
	return map[string]interface{}{
		"id":                        item.ID,
		"source_key":                item.SourceKey,
		"source_type":               item.SourceType,
		"external_id":               item.ExternalID,
		"canonical_url":             item.CanonicalURL,
		"title":                     item.Title,
		"author_handle":             item.AuthorHandle,
		"author_name":               item.AuthorName,
		"published_at":              item.PublishedAt,
		"saved_at":                  item.SavedAt,
		"language":                  item.Language,
		"primary_category":          item.PrimaryCategory,
		"primary_domain":            item.PrimaryDomain,
		"note_path":                 item.NotePath,
		"user_tags":                 item.UserTags,
		"x_post_status":             item.XPostStatus,
		"summary_status":            item.SummaryStatus,
		"summary_model":             item.SummaryModel,
		"summary_tool":              item.SummaryTool,
		"ocr_status":                item.OCRStatus,
		"ocr_model":                 item.OCRModel,
		"ocr_tool":                  item.OCRTool,
		"x_media_transcript_status": item.XMediaTranscriptStatus,
		"imported_at":               formatGetTime(item.ImportedAt),
		"updated_at":                formatGetTime(item.UpdatedAt),
		"last_seen_at":              formatGetTime(item.LastSeenAt),
	}
}

func slimSource(source model.SourceDocument) map[string]interface{} {
	return map[string]interface{}{
		"id":                    source.ID,
		"source_key":            source.SourceKey,
		"canonical_url":         source.CanonicalURL,
		"normalized_url":        source.NormalizedURL,
		"source_type":           source.SourceType,
		"domain":                source.Domain,
		"title":                 source.Title,
		"description":           source.Description,
		"site_name":             source.SiteName,
		"note_path":             source.NotePath,
		"extract_status":        source.ExtractStatus,
		"extract_error":         source.ExtractError,
		"extract_failure_kind":  source.ExtractFailureKind,
		"extract_failure_count": source.ExtractFailureCount,
		"extracted_at":          formatGetTime(source.ExtractedAt),
		"extract_tool":          source.ExtractTool,
		"extract_tool_version":  source.ExtractToolVersion,
		"summary_status":        source.SummaryStatus,
		"summary_error":         source.SummaryError,
		"summary_model":         source.SummaryModel,
		"summary_tool":          source.SummaryTool,
		"summary_tool_version":  source.SummaryToolVersion,
		"summarized_at":         formatGetTime(source.SummarizedAt),
		"content_hash":          source.ContentHash,
		"created_at":            formatGetTime(source.CreatedAt),
		"updated_at":            formatGetTime(source.UpdatedAt),
	}
}

func formatGetTime(value time.Time) string {
	if value.IsZero() {
		return ""
	}
	return value.UTC().Format(time.RFC3339)
}

func firstNonZeroTime(values ...time.Time) time.Time {
	for _, value := range values {
		if !value.IsZero() {
			return value
		}
	}
	return time.Time{}
}

func formatGetPayload(payload map[string]interface{}) string {
	var b strings.Builder
	_, _ = fmt.Fprintf(&b, "[%s] %s\n", payload["source_key"], payload["title"])
	if url, _ := payload["url"].(string); strings.TrimSpace(url) != "" {
		b.WriteString("URL: ")
		b.WriteString(url)
		b.WriteString("\n")
	}
	if note, _ := payload["note"].(string); strings.TrimSpace(note) != "" {
		b.WriteString("Note: ")
		b.WriteString(note)
		b.WriteString("\n")
	}
	b.WriteString("Content mode: ")
	b.WriteString(payload["content_mode"].(string))
	b.WriteString("\n")
	if query, _ := payload["query"].(string); strings.TrimSpace(query) != "" {
		b.WriteString("Query: ")
		b.WriteString(strings.TrimSpace(query))
		b.WriteString("\n")
	}

	if item, ok := payload["item"].(map[string]interface{}); ok {
		if tags, _ := item["user_tags"].(string); strings.TrimSpace(tags) != "" {
			b.WriteString("User tags: ")
			b.WriteString(strings.TrimSpace(tags))
			b.WriteString("\n")
		}
	}

	sections, _ := payload["content_sections"].([]getSection)
	if len(sections) == 0 {
		b.WriteString("\nNo content sections returned. Use content_mode=\"evidence\", \"raw\", or \"rendered\" for content.\n")
		return b.String()
	}
	for _, section := range sections {
		b.WriteString("\n## ")
		b.WriteString(section.Name)
		b.WriteString(" (")
		b.WriteString(section.Role)
		_, _ = fmt.Fprintf(&b, ", chars=%d", section.Chars)
		if section.Truncated {
			b.WriteString(", truncated")
		}
		b.WriteString(")\n")
		b.WriteString(section.Text)
		b.WriteString("\n")
	}
	return b.String()
}

func formatGetManyPayload(payload map[string]interface{}, texts []string) string {
	var b strings.Builder
	_, _ = fmt.Fprintf(&b, "Fetched %d of %d lookups", payload["count"], len(payload["lookups"].([]string)))
	if errors, ok := payload["errors"].([]getManyError); ok && len(errors) > 0 {
		_, _ = fmt.Fprintf(&b, " (%d errors)", len(errors))
	}
	b.WriteString(".\n")
	if query, _ := payload["query"].(string); strings.TrimSpace(query) != "" {
		b.WriteString("Query: ")
		b.WriteString(strings.TrimSpace(query))
		b.WriteString("\n")
	}
	if len(texts) > 0 {
		for i, text := range texts {
			if i > 0 {
				b.WriteString("\n---\n")
			}
			b.WriteString(strings.TrimSpace(text))
			b.WriteString("\n")
		}
	}
	if errors, ok := payload["errors"].([]getManyError); ok && len(errors) > 0 {
		b.WriteString("\nErrors:\n")
		for _, entry := range errors {
			b.WriteString("- ")
			b.WriteString(entry.Lookup)
			b.WriteString(": ")
			b.WriteString(entry.Error)
			b.WriteString("\n")
		}
	}
	return strings.TrimSpace(b.String())
}

func uniqueGetLookups(values []string) []string {
	seen := map[string]struct{}{}
	out := make([]string, 0, len(values))
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value == "" {
			continue
		}
		key := strings.ToLower(value)
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		out = append(out, value)
	}
	return out
}
