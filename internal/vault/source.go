package vault

import (
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
	body, err := RenderSource(source, backlinks)
	if err != nil {
		return err
	}
	return writeRenderedNote(cfg, source.NotePath, body, "source note")
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
