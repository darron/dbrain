package okf

import (
	"encoding/json"
	"fmt"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"

	"gopkg.in/yaml.v3"
)

func ValidateBundle(root string) (ValidationResult, error) {
	root = strings.TrimSpace(root)
	if root == "" {
		return ValidationResult{}, fmt.Errorf("bundle path is required")
	}
	absRoot, err := filepath.Abs(root)
	if err != nil {
		return ValidationResult{}, fmt.Errorf("resolve bundle path: %w", err)
	}
	result := ValidationResult{Bundle: absRoot, Conformant: true}
	manifest, _ := readManifest(absRoot)
	result.OmittedByFilterLinks = len(manifest.OmittedLinks)
	omitted := map[string]struct{}{}
	for _, link := range manifest.OmittedLinks {
		key := path.Clean(link.FromPath) + "->" + path.Clean(link.TargetPath)
		omitted[key] = struct{}{}
	}

	files, err := markdownFiles(absRoot)
	if err != nil {
		return ValidationResult{}, err
	}
	existing := map[string]struct{}{}
	for _, rel := range files {
		existing[rel] = struct{}{}
	}

	for _, rel := range files {
		full := filepath.Join(absRoot, filepath.FromSlash(rel))
		data, err := os.ReadFile(full)
		if err != nil {
			addValidationError(&result, rel, fmt.Sprintf("read: %v", err))
			continue
		}
		base := path.Base(rel)
		if base == "index.md" || base == "log.md" {
			if hasFrontmatter(data) {
				addValidationError(&result, rel, "reserved index/log file must not contain frontmatter")
			}
			if base == "index.md" {
				result.Indexes++
			}
			continue
		}
		result.Concepts++
		meta, _, err := parseMarkdownDocument(data)
		if err != nil {
			addValidationError(&result, rel, err.Error())
			continue
		}
		doc := ValidationDocument{Path: rel}
		if value, ok := meta["type"].(string); ok {
			doc.Type = strings.TrimSpace(value)
		}
		if value, ok := meta["title"].(string); ok {
			doc.Title = strings.TrimSpace(value)
		}
		if doc.Type == "" {
			doc.Error = "missing required frontmatter type"
			addValidationError(&result, rel, doc.Error)
		}
		result.Documents = append(result.Documents, doc)

		linkText := stripValidationSkippedRanges(string(data))
		for _, dest := range markdownDestinations(linkText) {
			if isExternalDestination(dest) {
				continue
			}
			target := resolveLinkTarget(rel, dest)
			if target == "" || !strings.HasSuffix(target, ".md") {
				continue
			}
			if _, ok := existing[target]; ok {
				continue
			}
			key := path.Clean(rel) + "->" + path.Clean(target)
			if _, ok := omitted[key]; ok {
				continue
			}
			result.BrokenInternalLinks++
			addValidationError(&result, rel, "broken internal link: "+dest)
		}
	}
	result.Conformant = len(result.Errors) == 0
	return result, nil
}

func stripValidationSkippedRanges(text string) string {
	var out strings.Builder
	for {
		start := strings.Index(text, validationSkipBegin)
		if start < 0 {
			out.WriteString(text)
			return out.String()
		}
		out.WriteString(text[:start])
		afterStart := text[start+len(validationSkipBegin):]
		end := strings.Index(afterStart, validationSkipEnd)
		if end < 0 {
			return out.String()
		}
		text = afterStart[end+len(validationSkipEnd):]
	}
}

func markdownFiles(root string) ([]string, error) {
	var files []string
	err := filepath.WalkDir(root, func(full string, entry os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.IsDir() {
			return nil
		}
		if filepath.Ext(entry.Name()) != ".md" {
			return nil
		}
		rel, err := filepath.Rel(root, full)
		if err != nil {
			return err
		}
		files = append(files, filepath.ToSlash(rel))
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("walk bundle: %w", err)
	}
	sort.Strings(files)
	return files, nil
}

func hasFrontmatter(data []byte) bool {
	return strings.HasPrefix(string(data), "---\n")
}

func parseMarkdownDocument(data []byte) (map[string]any, string, error) {
	text := string(data)
	if !strings.HasPrefix(text, "---\n") {
		return nil, "", fmt.Errorf("missing YAML frontmatter")
	}
	rest := text[len("---\n"):]
	idx := strings.Index(rest, "\n---\n")
	if idx < 0 {
		return nil, "", fmt.Errorf("unterminated YAML frontmatter")
	}
	rawYAML := rest[:idx]
	body := rest[idx+len("\n---\n"):]
	meta := map[string]any{}
	if err := yaml.Unmarshal([]byte(rawYAML), &meta); err != nil {
		return nil, "", fmt.Errorf("parse YAML frontmatter: %w", err)
	}
	return meta, body, nil
}

func markdownDestinations(text string) []string {
	var out []string
	for i := 0; i < len(text); i++ {
		if text[i] != ']' || i+1 >= len(text) || text[i+1] != '(' {
			continue
		}
		start := i + 2
		end := start
		depth := 0
		for end < len(text) {
			switch text[end] {
			case '(':
				depth++
			case ')':
				if depth == 0 {
					dest := strings.TrimSpace(text[start:end])
					if strings.HasPrefix(dest, "<") && strings.HasSuffix(dest, ">") {
						dest = strings.TrimSuffix(strings.TrimPrefix(dest, "<"), ">")
					}
					out = append(out, dest)
					i = end
					goto next
				}
				depth--
			}
			end++
		}
	next:
	}
	return out
}

func resolveLinkTarget(fromPath, dest string) string {
	dest = stripLinkFragment(strings.TrimSpace(dest))
	if dest == "" {
		return ""
	}
	if path.IsAbs(dest) {
		dest = strings.TrimPrefix(dest, "/")
		return path.Clean(dest)
	}
	return path.Clean(path.Join(path.Dir(fromPath), dest))
}

func addValidationError(result *ValidationResult, rel string, msg string) {
	result.Errors = append(result.Errors, rel+": "+msg)
}

func readManifest(root string) (Manifest, error) {
	data, err := os.ReadFile(filepath.Join(root, manifestFileName))
	if err != nil {
		return Manifest{}, err
	}
	var manifest Manifest
	if err := json.Unmarshal(data, &manifest); err != nil {
		return Manifest{}, err
	}
	return manifest, nil
}
