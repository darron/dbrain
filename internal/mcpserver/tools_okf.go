package mcpserver

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/darron/dbrain/internal/okf"
)

const (
	defaultOKFGetChars = 8000
	maxOKFGetChars     = 50000
)

func (s *Server) toolOKFSearch(raw json.RawMessage) (map[string]interface{}, error) {
	var args struct {
		Query string `json:"query"`
		Limit int    `json:"limit"`
	}
	if err := json.Unmarshal(raw, &args); err != nil {
		return nil, fmt.Errorf("decode okf search args: %w", err)
	}
	limit := defaultInt(args.Limit, 10)
	results, err := okf.SearchBundle(s.cfg.OKFDir, args.Query, limit)
	if err != nil {
		return nil, err
	}
	return toolOKResult(formatOKFSearchResults(s.cfg.OKFDir, args.Query, results), map[string]interface{}{
		"bundle":  s.cfg.OKFDir,
		"query":   strings.TrimSpace(args.Query),
		"count":   len(results),
		"results": results,
	}), nil
}

func (s *Server) toolOKFGet(raw json.RawMessage) (map[string]interface{}, error) {
	var args struct {
		Lookup          string `json:"lookup"`
		IncludeMarkdown bool   `json:"include_markdown"`
		MaxChars        int    `json:"max_chars"`
	}
	if err := json.Unmarshal(raw, &args); err != nil {
		return nil, fmt.Errorf("decode okf get args: %w", err)
	}
	maxChars := boundedOKFMaxChars(args.MaxChars)
	concept, err := okf.GetBundleConcept(s.cfg.OKFDir, args.Lookup)
	if err != nil {
		return nil, err
	}
	body, bodyTruncated := truncateOKFMCPText(concept.Body, maxChars)
	markdown := ""
	markdownTruncated := false
	if args.IncludeMarkdown {
		markdown, markdownTruncated = truncateOKFMCPText(concept.Markdown, maxChars)
	}
	concept.Body = body
	concept.Markdown = markdown
	payload := map[string]interface{}{
		"bundle":             s.cfg.OKFDir,
		"include_markdown":   args.IncludeMarkdown,
		"max_chars":          maxChars,
		"body_truncated":     bodyTruncated,
		"markdown_truncated": markdownTruncated,
		"concept":            concept,
	}
	return toolOKResult(formatOKFConcept(concept, bodyTruncated, markdownTruncated), payload), nil
}

func boundedOKFMaxChars(value int) int {
	if value <= 0 {
		return defaultOKFGetChars
	}
	if value > maxOKFGetChars {
		return maxOKFGetChars
	}
	return value
}

func truncateOKFMCPText(value string, maxChars int) (string, bool) {
	value = strings.TrimSpace(value)
	if value == "" {
		return "", false
	}
	runes := []rune(value)
	if len(runes) <= maxChars {
		return value, false
	}
	return string(runes[:maxChars]) + "\n\n[Truncated by dbrain_okf_get max_chars.]", true
}

func formatOKFSearchResults(bundlePath, query string, results []okf.SearchResult) string {
	var b strings.Builder
	_, _ = fmt.Fprintf(&b, "OKF bundle: %s\n", bundlePath)
	if strings.TrimSpace(query) != "" {
		_, _ = fmt.Fprintf(&b, "Query: %s\n", strings.TrimSpace(query))
	}
	_, _ = fmt.Fprintf(&b, "Results: %d\n", len(results))
	for _, result := range results {
		_, _ = fmt.Fprintf(&b, "- [%s] %s (%s) id=%s", result.Type, result.Title, result.Path, result.ConceptID)
		if strings.TrimSpace(result.SourceKey) != "" {
			_, _ = fmt.Fprintf(&b, " source_key=%s", result.SourceKey)
		}
		b.WriteString("\n")
		if strings.TrimSpace(result.Snippet) != "" {
			_, _ = fmt.Fprintf(&b, "  %s\n", result.Snippet)
		}
	}
	return strings.TrimSpace(b.String())
}

func formatOKFConcept(concept okf.Concept, bodyTruncated, markdownTruncated bool) string {
	var b strings.Builder
	_, _ = fmt.Fprintf(&b, "OKF concept: [%s] %s\n", concept.Type, concept.Title)
	_, _ = fmt.Fprintf(&b, "Path: %s\n", concept.Path)
	_, _ = fmt.Fprintf(&b, "Concept ID: %s\n", concept.ConceptID)
	if strings.TrimSpace(concept.SourceKey) != "" {
		_, _ = fmt.Fprintf(&b, "Source key: %s\n", concept.SourceKey)
	}
	if strings.TrimSpace(concept.Description) != "" {
		_, _ = fmt.Fprintf(&b, "\n%s\n", concept.Description)
	}
	if strings.TrimSpace(concept.Body) != "" {
		b.WriteString("\n")
		b.WriteString(concept.Body)
		if bodyTruncated {
			b.WriteString("\n")
		}
	}
	if markdownTruncated {
		b.WriteString("\nMarkdown was truncated in structured output.\n")
	}
	return strings.TrimSpace(b.String())
}
