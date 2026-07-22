package okf

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path"
	"path/filepath"
	"strings"

	"github.com/darron/dbrain/internal/vaultfs"
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
	confined, err := vaultfs.Open(absRoot)
	if err != nil {
		return ValidationResult{}, fmt.Errorf("open bundle root: %w", err)
	}
	defer func() { _ = confined.Close() }()
	_, result, err := inspectBundleDetailed(context.Background(), confined)
	if err != nil {
		return ValidationResult{}, err
	}
	result.Bundle = absRoot
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
