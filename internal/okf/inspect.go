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

// InspectManifest reads and validates only the bundle manifest. It deliberately
// does not inspect, open, or traverse any Markdown concept target.
func InspectManifest(ctx context.Context, root *vaultfs.Root) (InspectionSummary, error) {
	if root == nil {
		return InspectionSummary{}, fmt.Errorf("bundle root is required")
	}
	if err := ctx.Err(); err != nil {
		return InspectionSummary{}, err
	}
	summary := InspectionSummary{}
	metadata, err := root.Inspect(manifestFileName)
	if err != nil || !metadata.Regular {
		summary.ValidationErrorCount = 1
		return summary, nil
	}
	data, err := root.ReadFile(manifestFileName)
	if err != nil {
		summary.ValidationErrorCount = 1
		return summary, nil
	}
	var manifest Manifest
	if err := json.Unmarshal(data, &manifest); err != nil {
		summary.ValidationErrorCount = 1
		return summary, nil
	}
	exportedAt, err := time.Parse(time.RFC3339, strings.TrimSpace(manifest.ExportedAt))
	if err != nil || exportedAt.IsZero() {
		summary.ValidationErrorCount++
	} else {
		summary.ExportedAt = exportedAt.UTC()
	}
	identityValid := isSupportedOKFVersion(manifest.OKFVersion) && manifest.Profile == ProfilePrivate
	if !identityValid {
		summary.ValidationErrorCount++
	}
	pathsValid := true
	seen := make(map[string]struct{}, len(manifest.Concepts))
	for _, concept := range manifest.Concepts {
		if err := ValidateConceptPath(concept.Path); err != nil {
			pathsValid = false
			summary.ValidationErrorCount++
			continue
		}
		key := manifestCollisionKey(concept.Path)
		if _, duplicate := seen[key]; duplicate {
			pathsValid = false
			summary.ValidationErrorCount++
			continue
		}
		seen[key] = struct{}{}
	}
	summary.DocumentCount = len(manifest.Concepts)
	summary.ManifestValid = !summary.ExportedAt.IsZero() && identityValid && pathsValid
	return summary, nil
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

	manifestMetadata, err := root.Inspect(manifestFileName)
	if err != nil || !manifestMetadata.Regular {
		summary.ValidationErrorCount = 1
		result.Conformant = false
		result.Errors = append(result.Errors, "manifest is missing or unreadable")
		return summary, result, nil
	}
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
	manifestIdentityValid := true
	if !isSupportedOKFVersion(manifest.OKFVersion) {
		manifestIdentityValid = false
		summary.ValidationErrorCount++
		result.Errors = append(result.Errors, "manifest okf_version is missing or unsupported")
	}
	if manifest.Profile != ProfilePrivate {
		manifestIdentityValid = false
		summary.ValidationErrorCount++
		result.Errors = append(result.Errors, "manifest profile is missing or unsupported")
	}
	manifestPathsValid := true
	seenManifestPaths := make(map[string]struct{}, len(manifest.Concepts))
	for _, concept := range manifest.Concepts {
		if err := ValidateConceptPath(concept.Path); err != nil {
			manifestPathsValid = false
			summary.ValidationErrorCount++
			result.Errors = append(result.Errors, "manifest contains an invalid concept path")
			continue
		}
		collisionKey := manifestCollisionKey(concept.Path)
		if _, duplicate := seenManifestPaths[collisionKey]; duplicate {
			manifestPathsValid = false
			summary.ValidationErrorCount++
			result.Errors = append(result.Errors, "manifest contains a duplicate concept path")
			continue
		}
		seenManifestPaths[collisionKey] = struct{}{}
		metadata, inspectErr := root.Inspect(concept.Path)
		if inspectErr != nil {
			manifestPathsValid = false
			summary.ValidationErrorCount++
			result.Errors = append(result.Errors, "manifest concept path is missing, unreadable, or outside the bundle")
			var logicalErr *vaultfs.LogicalFileError
			if !errors.As(inspectErr, &logicalErr) || logicalErr.Code != "missing" {
				summary.TraversalComplete = false
			}
			continue
		}
		if !metadata.Regular {
			manifestPathsValid = false
			summary.ValidationErrorCount++
			result.Errors = append(result.Errors, "manifest concept target is not a regular file")
		}
	}
	summary.ManifestValid = err == nil && !exportedAt.IsZero() && manifestIdentityValid && manifestPathsValid
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
				if base != "index.md" || rel != "index.md" {
					summary.ValidationErrorCount++
					addValidationError(&result, rel, "reserved index/log file must not contain frontmatter")
				} else {
					meta, _, parseErr := parseMarkdownDocument(data)
					version, _ := meta["okf_version"].(string)
					if parseErr != nil || len(meta) != 1 || !isSupportedOKFVersion(version) || version != manifest.OKFVersion {
						summary.ValidationErrorCount++
						addValidationError(&result, rel, "root index frontmatter must contain only the matching okf_version")
					}
				}
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
		}
	}
	result.BrokenInternalLinks = summary.BrokenLinkCount
	result.Conformant = summary.ManifestValid && summary.TraversalComplete && summary.ValidationErrorCount == 0
	return summary, result, nil
}

func isSupportedOKFVersion(version string) bool {
	switch strings.TrimSpace(version) {
	case legacyOKFVersion, OKFVersion:
		return true
	default:
		return false
	}
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
			if path.Ext(entry.Name()) == ".md" {
				metadata, inspectErr := root.Inspect(rel)
				if inspectErr != nil || !metadata.Regular {
					errorsCount++
					continue
				}
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
