package mcpserver

import (
	"sort"
	"strings"
	"time"
	"unicode"

	"github.com/darron/dbrain/internal/model"
)

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

func sourceAvailableSections(source model.SourceDocument) []getSection {
	var sections []getSection
	appendUniqueSection(&sections, makeGetSection("user_tags", "metadata", "", "", "", time.Time{}, source.UserTags, 0))
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
	lower := strings.ToLower(text)
	type candidateWindow struct {
		start int
		end   int
		score int
	}
	best := candidateWindow{start: -1}
	for _, term := range terms {
		searchFrom := 0
		for {
			idx := strings.Index(lower[searchFrom:], term)
			if idx < 0 {
				break
			}
			idx += searchFrom
			window := queryWindowBounds(lower, idx, maxChars)
			windowText := lower[window.start:window.end]
			score := queryWindowScore(windowText, lower, terms, term)
			if best.start < 0 || score > best.score || (score == best.score && window.start < best.start) {
				best = candidateWindow{start: window.start, end: window.end, score: score}
			}
			searchFrom = idx + len(term)
			if searchFrom >= len(lower) {
				break
			}
		}
	}
	if best.start < 0 {
		return "", false, false
	}
	runes := []rune(text)
	if maxChars <= 0 || len(runes) <= maxChars {
		return text, false, true
	}

	startRune := len([]rune(lower[:best.start]))
	endRune := len([]rune(lower[:best.end]))
	snippet := strings.TrimSpace(string(runes[startRune:endRune]))
	if startRune > 0 {
		snippet = "..." + snippet
	}
	if endRune < len(runes) {
		snippet += "..."
	}
	return snippet, startRune > 0 || endRune < len(runes), true
}

func queryWindowBounds(value string, matchByteIndex int, maxChars int) struct{ start, end int } {
	runes := []rune(value)
	if maxChars <= 0 || len(runes) <= maxChars {
		return struct{ start, end int }{start: 0, end: len(value)}
	}
	bodyMax := maxChars - 6
	if bodyMax < 40 {
		bodyMax = maxChars
	}
	if bodyMax <= 0 {
		bodyMax = maxChars
	}
	matchRune := len([]rune(value[:matchByteIndex]))
	startRune := matchRune - bodyMax/3
	if startRune < 0 {
		startRune = 0
	}
	endRune := startRune + bodyMax
	if endRune > len(runes) {
		endRune = len(runes)
		startRune = endRune - bodyMax
		if startRune < 0 {
			startRune = 0
		}
	}
	startByte := len(string(runes[:startRune]))
	endByte := len(string(runes[:endRune]))
	return struct{ start, end int }{start: startByte, end: endByte}
}

func queryWindowScore(window string, fullText string, terms []string, matchedTerm string) int {
	score := len([]rune(matchedTerm))
	for _, term := range queryWindowCoverageTerms(terms) {
		if strings.Contains(window, term) {
			occurrences := strings.Count(fullText, term)
			if occurrences <= 0 {
				occurrences = 1
			}
			score += 100 + 1000/occurrences + len([]rune(term))
		}
	}
	return score
}

func queryWindowCoverageTerms(terms []string) []string {
	var out []string
	seen := map[string]struct{}{}
	for _, term := range terms {
		if strings.ContainsAny(term, " -") {
			continue
		}
		if _, ok := seen[term]; ok {
			continue
		}
		seen[term] = struct{}{}
		out = append(out, term)
	}
	return out
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
	sort.SliceStable(terms, func(i, j int) bool {
		return len([]rune(terms[i])) > len([]rune(terms[j]))
	})
	return terms
}
