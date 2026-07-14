package okf

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"path"
	"sort"
	"strings"
	"time"

	"github.com/darron/dbrain/internal/vaultfs"
)

type InspectionSummary struct {
	ManifestValid        bool      `json:"manifest_valid"`
	ExportedAt           time.Time `json:"exported_at"`
	DocumentCount        int       `json:"document_count"`
	BrokenLinkCount      int       `json:"broken_link_count"`
	ValidationErrorCount int       `json:"validation_error_count"`
	TraversalComplete    bool      `json:"traversal_complete"`
}

func InspectBundle(ctx context.Context, root *vaultfs.Root) (InspectionSummary, error) {
	summary, _, err := inspectBundleDetailed(ctx, root)
	return summary, err
}

func inspectBundleDetailed(ctx context.Context, root *vaultfs.Root) (InspectionSummary, ValidationResult, error) {
	if root == nil {
		return InspectionSummary{}, ValidationResult{}, fmt.Errorf("bundle root is required")
	}
	if err := ctx.Err(); err != nil {
		return InspectionSummary{}, ValidationResult{}, err
	}
	summary := InspectionSummary{TraversalComplete: true}
	result := ValidationResult{Conformant: true}

	manifestData, err := root.ReadFile(manifestFileName)
	if err != nil {
		summary.ValidationErrorCount = 1
		result.Conformant = false
		result.Errors = append(result.Errors, "manifest is missing or unreadable")
		return summary, result, nil
	}
	var manifest Manifest
	if err := json.Unmarshal(manifestData, &manifest); err != nil {
		summary.ValidationErrorCount = 1
		result.Conformant = false
		result.Errors = append(result.Errors, "manifest is invalid")
		return summary, result, nil
	}
	exportedAt, err := time.Parse(time.RFC3339, strings.TrimSpace(manifest.ExportedAt))
	if err != nil || exportedAt.IsZero() {
		summary.ValidationErrorCount++
		result.Errors = append(result.Errors, "manifest exported_at is missing or invalid")
	} else {
		summary.ExportedAt = exportedAt.UTC()
	}
	manifestPathsValid := true
	for _, concept := range manifest.Concepts {
		if !safeBundleLogicalPath(concept.Path) {
			manifestPathsValid = false
			summary.ValidationErrorCount++
			result.Errors = append(result.Errors, "manifest contains an unsafe concept path")
			continue
		}
		if _, inspectErr := root.Inspect(concept.Path); inspectErr != nil {
			manifestPathsValid = false
			summary.ValidationErrorCount++
			result.Errors = append(result.Errors, "manifest concept path is missing, unreadable, or outside the bundle")
			var logicalErr *vaultfs.LogicalFileError
			if !errors.As(inspectErr, &logicalErr) || logicalErr.Code != "missing" {
				summary.TraversalComplete = false
			}
		}
	}
	summary.ManifestValid = err == nil && !exportedAt.IsZero() && manifestPathsValid
	result.OmittedByFilterLinks = len(manifest.OmittedLinks)

	files, traversalErrors, err := walkBundleMarkdown(ctx, root)
	if err != nil {
		return InspectionSummary{}, ValidationResult{}, err
	}
	if traversalErrors > 0 {
		summary.TraversalComplete = false
		summary.ValidationErrorCount += traversalErrors
		for range traversalErrors {
			result.Errors = append(result.Errors, "bundle entry is unreadable or outside the bundle")
		}
	}
	existing := make(map[string]struct{}, len(files))
	for _, rel := range files {
		existing[rel] = struct{}{}
	}
	omitted := make(map[string]struct{}, len(manifest.OmittedLinks))
	for _, link := range manifest.OmittedLinks {
		omitted[path.Clean(link.FromPath)+"->"+path.Clean(link.TargetPath)] = struct{}{}
	}

	for _, rel := range files {
		if err := ctx.Err(); err != nil {
			return InspectionSummary{}, ValidationResult{}, err
		}
		data, err := root.ReadFile(rel)
		if err != nil {
			summary.TraversalComplete = false
			summary.ValidationErrorCount++
			addValidationError(&result, rel, "unreadable or outside bundle")
			continue
		}
		base := path.Base(rel)
		if base == "index.md" || base == "log.md" {
			if hasFrontmatter(data) {
				summary.ValidationErrorCount++
				addValidationError(&result, rel, "reserved index/log file must not contain frontmatter")
			}
			if base == "index.md" {
				result.Indexes++
			}
			continue
		}
		summary.DocumentCount++
		result.Concepts++
		meta, _, err := parseMarkdownDocument(data)
		if err != nil {
			summary.ValidationErrorCount++
			addValidationError(&result, rel, err.Error())
			continue
		}
		doc := ValidationDocument{Path: rel}
		doc.Type, _ = meta["type"].(string)
		doc.Title, _ = meta["title"].(string)
		if strings.TrimSpace(doc.Type) == "" {
			doc.Error = "missing required frontmatter type"
			summary.ValidationErrorCount++
			addValidationError(&result, rel, doc.Error)
		}
		result.Documents = append(result.Documents, doc)
		for _, dest := range markdownDestinations(stripValidationSkippedRanges(string(data))) {
			if isExternalDestination(dest) {
				continue
			}
			if path.IsAbs(stripLinkFragment(strings.TrimSpace(dest))) {
				summary.BrokenLinkCount++
				summary.ValidationErrorCount++
				addValidationError(&result, rel, "absolute internal link is not allowed")
				continue
			}
			target := resolveLinkTarget(rel, dest)
			if target == "" || !strings.HasSuffix(target, ".md") {
				continue
			}
			if _, ok := existing[target]; ok {
				continue
			}
			if _, ok := omitted[path.Clean(rel)+"->"+path.Clean(target)]; ok {
				continue
			}
			summary.BrokenLinkCount++
			summary.ValidationErrorCount++
			addValidationError(&result, rel, "broken internal link: "+dest)
		}
	}
	result.BrokenInternalLinks = summary.BrokenLinkCount
	result.Conformant = summary.ManifestValid && summary.TraversalComplete && summary.ValidationErrorCount == 0
	return summary, result, nil
}

func walkBundleMarkdown(ctx context.Context, root *vaultfs.Root) ([]string, int, error) {
	var files []string
	errorsCount := 0
	var walk func(string) error
	walk = func(dir string) error {
		if err := ctx.Err(); err != nil {
			return err
		}
		opened, err := root.Open(dir)
		if err != nil {
			errorsCount++
			return nil
		}
		entries, err := opened.ReadDir(-1)
		_ = opened.Close()
		if err != nil {
			errorsCount++
			return nil
		}
		for _, entry := range entries {
			rel := entry.Name()
			if dir != "." {
				rel = path.Join(dir, rel)
			}
			if entry.Type()&fs.ModeSymlink != 0 && path.Ext(entry.Name()) != ".md" {
				errorsCount++
				continue
			}
			if entry.IsDir() {
				if err := walk(rel); err != nil {
					return err
				}
				continue
			}
			if path.Ext(entry.Name()) == ".md" || entry.Type()&fs.ModeSymlink != 0 && path.Ext(entry.Name()) == ".md" {
				files = append(files, rel)
			}
		}
		return nil
	}
	if err := walk("."); err != nil {
		return nil, 0, err
	}
	sort.Strings(files)
	return files, errorsCount, nil
}

func safeBundleLogicalPath(name string) bool {
	name = strings.TrimSpace(name)
	if name == "" || path.IsAbs(name) {
		return false
	}
	clean := path.Clean(name)
	return clean != ".." && !strings.HasPrefix(clean, "../")
}
