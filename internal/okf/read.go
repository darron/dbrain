package okf

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

func SearchBundle(root string, query string, limit int) ([]SearchResult, error) {
	root = strings.TrimSpace(root)
	if root == "" {
		return nil, fmt.Errorf("bundle path is required")
	}
	if limit <= 0 {
		limit = 10
	}
	query = strings.ToLower(strings.TrimSpace(query))
	manifest, err := readManifest(root)
	if err != nil {
		return nil, fmt.Errorf("read okf manifest: %w", err)
	}
	concepts := manifest.Concepts
	sort.Slice(concepts, func(i, j int) bool { return concepts[i].Path < concepts[j].Path })
	results := make([]SearchResult, 0, min(len(concepts), limit))
	for _, concept := range concepts {
		full := filepath.Join(root, filepath.FromSlash(concept.Path))
		data, err := os.ReadFile(full)
		if err != nil {
			continue
		}
		text := strings.ToLower(concept.Title + "\n" + concept.Description + "\n" + string(data))
		if query != "" && !strings.Contains(text, query) {
			continue
		}
		results = append(results, SearchResult{
			Path:        concept.Path,
			Type:        concept.Type,
			Title:       concept.Title,
			Description: concept.Description,
			ConceptID:   concept.ConceptID,
			SourceKey:   concept.SourceKey,
			SourceType:  concept.SourceType,
			Snippet:     snippetForQuery(string(data), query),
		})
		if len(results) >= limit {
			break
		}
	}
	return results, nil
}

func GetBundleConcept(root string, lookup string) (Concept, error) {
	root = strings.TrimSpace(root)
	lookup = strings.TrimSpace(lookup)
	if root == "" {
		return Concept{}, fmt.Errorf("bundle path is required")
	}
	if lookup == "" {
		return Concept{}, fmt.Errorf("lookup is required")
	}
	manifest, err := readManifest(root)
	if err != nil {
		return Concept{}, fmt.Errorf("read okf manifest: %w", err)
	}
	var selected ManifestConcept
	for _, concept := range manifest.Concepts {
		if concept.Path == lookup || concept.ConceptID == lookup || concept.SourceKey == lookup {
			selected = concept
			break
		}
	}
	if selected.Path == "" {
		return Concept{}, fmt.Errorf("okf concept not found: %s", lookup)
	}
	full := filepath.Join(root, filepath.FromSlash(selected.Path))
	data, err := os.ReadFile(full)
	if err != nil {
		return Concept{}, fmt.Errorf("read okf concept %s: %w", selected.Path, err)
	}
	frontmatter, body, err := parseMarkdownDocument(data)
	if err != nil {
		return Concept{}, fmt.Errorf("parse okf concept %s: %w", selected.Path, err)
	}
	return Concept{
		Path:        selected.Path,
		Type:        selected.Type,
		Title:       selected.Title,
		Description: selected.Description,
		ConceptID:   selected.ConceptID,
		SourceKey:   selected.SourceKey,
		SourceType:  selected.SourceType,
		Frontmatter: frontmatter,
		Markdown:    string(data),
		Body:        body,
	}, nil
}

func snippetForQuery(text string, query string) string {
	text = cleanText(text)
	if text == "" {
		return ""
	}
	if query == "" {
		runes := []rune(text)
		if len(runes) > 240 {
			return string(runes[:240]) + "..."
		}
		return text
	}
	lower := strings.ToLower(text)
	idx := strings.Index(lower, query)
	if idx < 0 {
		return ""
	}
	start := idx - 100
	if start < 0 {
		start = 0
	}
	end := idx + len(query) + 140
	if end > len(text) {
		end = len(text)
	}
	snippet := strings.TrimSpace(text[start:end])
	if start > 0 {
		snippet = "..." + snippet
	}
	if end < len(text) {
		snippet += "..."
	}
	return snippet
}
