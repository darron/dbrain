package retrieval

import (
	"strings"
	"time"
)

func NewContentSection(name, role, status, modelName, tool string, at time.Time, text string, maxChars int) ContentSection {
	trimmed := strings.TrimSpace(text)
	section := ContentSection{
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
		section.Text, section.Truncated = TruncateText(trimmed, maxChars)
	} else {
		section.Text = trimmed
	}
	return section
}

func AppendUniqueContentSection(sections *[]ContentSection, section ContentSection) {
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

func ContentSectionCatalog(sections []ContentSection) []ContentSection {
	catalog := make([]ContentSection, 0, len(sections))
	for _, section := range sections {
		section.Text = ""
		section.Truncated = false
		catalog = append(catalog, section)
	}
	return catalog
}

func TruncateText(value string, maxChars int) (string, bool) {
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
