package vault

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/darron/dbrain/internal/config"
	"github.com/darron/dbrain/internal/model"
)

func SourceNoteRelativePath(sourceType, slug string) string {
	if sourceType == "" {
		sourceType = "web"
	}
	if slug == "" {
		slug = "unknown"
	}
	return filepath.ToSlash(filepath.Join("sources", sourceType, slug+".md"))
}

func WriteSource(cfg config.Config, source model.SourceDocument, backlinks []model.SourceBacklink) error {
	fullPath := filepath.Join(cfg.VaultDir, filepath.FromSlash(source.NotePath))
	if err := os.MkdirAll(filepath.Dir(fullPath), 0o755); err != nil {
		return fmt.Errorf("create source note dir: %w", err)
	}

	body, err := RenderSource(source, backlinks)
	if err != nil {
		return err
	}

	existing, err := os.ReadFile(fullPath)
	if err == nil && string(existing) == body {
		return nil
	}
	if err := os.WriteFile(fullPath, []byte(body), 0o644); err != nil {
		return fmt.Errorf("write source note: %w", err)
	}
	return nil
}

func RenderSource(source model.SourceDocument, backlinks []model.SourceBacklink) (string, error) {
	var b strings.Builder
	writeSourceFrontmatter(&b, source)

	title := strings.TrimSpace(source.Title)
	if title == "" {
		title = source.CanonicalURL
	}
	b.WriteString("# ")
	b.WriteString(title)
	b.WriteString("\n\n")

	writeSourceDetailsSection(&b, source)
	writeSourceTextSections(&b, source)
	writeSourceBacklinksSection(&b, backlinks)
	return b.String(), nil
}
