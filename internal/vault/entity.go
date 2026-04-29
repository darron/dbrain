package vault

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"unicode"

	"github.com/darron/dbrain/internal/config"
	"github.com/darron/dbrain/internal/entities"
)

func EntityIndexRelativePath() string {
	return filepath.ToSlash(filepath.Join("entities", "index.md"))
}

func WriteEntity(cfg config.Config, entity entities.Entity) error {
	fullPath := filepath.Join(cfg.VaultDir, filepath.FromSlash(entity.NotePath))
	if err := os.MkdirAll(filepath.Dir(fullPath), 0o755); err != nil {
		return fmt.Errorf("create entity note dir: %w", err)
	}

	body := RenderEntity(entity)
	existing, err := os.ReadFile(fullPath)
	if err == nil && string(existing) == body {
		return nil
	}
	if err := os.WriteFile(fullPath, []byte(body), 0o644); err != nil {
		return fmt.Errorf("write entity note: %w", err)
	}
	return nil
}

func WriteEntityIndex(cfg config.Config, entitiesList []entities.Entity) error {
	fullPath := filepath.Join(cfg.VaultDir, filepath.FromSlash(EntityIndexRelativePath()))
	if err := os.MkdirAll(filepath.Dir(fullPath), 0o755); err != nil {
		return fmt.Errorf("create entity index dir: %w", err)
	}

	body := RenderEntityIndex(entitiesList)
	existing, err := os.ReadFile(fullPath)
	if err == nil && string(existing) == body {
		return nil
	}
	if err := os.WriteFile(fullPath, []byte(body), 0o644); err != nil {
		return fmt.Errorf("write entity index: %w", err)
	}
	return nil
}

func RenderEntity(entity entities.Entity) string {
	var b strings.Builder

	b.WriteString("---\n")
	writeYAMLScalar(&b, "brain_entity_key", entity.Key)
	writeYAMLScalar(&b, "entity_kind", string(entity.Kind))
	writeYAMLScalar(&b, "canonical_url", entity.CanonicalURL)
	writeYAMLScalar(&b, "domain", entity.Domain)
	writeYAMLScalar(&b, "reference_count", strconv.Itoa(entity.ReferenceCount))
	writeYAMLArray(&b, "source_types", entity.SourceTypes)
	writeYAMLArray(&b, "aliases", entity.Aliases)
	writeYAMLArray(&b, "tags", []string{"source/entity", "entity/" + string(entity.Kind)})
	b.WriteString("---\n\n")

	b.WriteString("# ")
	b.WriteString(entity.Name)
	b.WriteString("\n\n")

	b.WriteString("## Summary\n\n")
	_, _ = fmt.Fprintf(&b, "Derived `%s` entity note from %d references in the local brain.\n", entity.Kind, entity.ReferenceCount)

	if len(entity.Aliases) > 0 {
		b.WriteString("\n## Aliases\n\n")
		for _, alias := range entity.Aliases {
			b.WriteString("- ")
			b.WriteString(alias)
			b.WriteString("\n")
		}
	}

	if len(entity.Links) > 0 {
		b.WriteString("\n## Related Entities\n\n")
		for _, link := range entity.Links {
			b.WriteString("- ")
			b.WriteString(renderEntityLink(link.NotePath, link.Name))
			b.WriteString(" (`")
			b.WriteString(link.Relationship)
			b.WriteString("`, `")
			b.WriteString(string(link.Kind))
			b.WriteString("`)\n")
		}
	}

	if len(entity.References) > 0 {
		b.WriteString("\n## Referenced By\n\n")
		for _, ref := range entity.References {
			b.WriteString("- ")
			if strings.TrimSpace(ref.NotePath) != "" {
				b.WriteString(obsidianLink(ref.NotePath, firstNonEmptyEntityValue(ref.Title, ref.SourceKey)))
			} else {
				b.WriteString(firstNonEmptyEntityValue(ref.Title, ref.SourceKey))
			}
			b.WriteString(" (`")
			b.WriteString(ref.Relationship)
			b.WriteString("`, `")
			b.WriteString(ref.SourceType)
			b.WriteString("`)")
			if strings.TrimSpace(ref.URL) != "" {
				b.WriteString(" ")
				b.WriteString(ref.URL)
			}
			b.WriteString("\n")
		}
	}

	return b.String()
}

func RenderEntityIndex(entitiesList []entities.Entity) string {
	grouped := map[entities.Kind][]entities.Entity{}
	for _, entity := range entitiesList {
		grouped[entity.Kind] = append(grouped[entity.Kind], entity)
	}

	var kinds []entities.Kind
	for kind := range grouped {
		kinds = append(kinds, kind)
	}
	sort.Slice(kinds, func(i, j int) bool { return kinds[i] < kinds[j] })

	var b strings.Builder
	b.WriteString("---\n")
	writeYAMLScalar(&b, "brain_entity_index", "true")
	writeYAMLScalar(&b, "entity_count", strconv.Itoa(len(entitiesList)))
	writeYAMLArray(&b, "tags", []string{"source/entity-index"})
	b.WriteString("---\n\n")

	b.WriteString("# Entity Index\n\n")
	b.WriteString("Browsable index of derived entities in the local brain.\n")

	if len(entitiesList) == 0 {
		b.WriteString("\nNo entities have been derived yet.\n")
		return b.String()
	}

	for _, kind := range kinds {
		group := grouped[kind]
		sort.Slice(group, func(i, j int) bool {
			if group[i].ReferenceCount != group[j].ReferenceCount {
				return group[i].ReferenceCount > group[j].ReferenceCount
			}
			return strings.ToLower(group[i].Name) < strings.ToLower(group[j].Name)
		})

		b.WriteString("\n## ")
		b.WriteString(entityHeading(kind))
		b.WriteString("\n\n")
		for _, entity := range group {
			b.WriteString("- ")
			b.WriteString(renderEntityLink(entity.NotePath, entity.Name))
			b.WriteString(" (`")
			b.WriteString(strconv.Itoa(entity.ReferenceCount))
			b.WriteString("` refs")
			if entity.Domain != "" {
				b.WriteString(", `")
				b.WriteString(entity.Domain)
				b.WriteString("`")
			}
			b.WriteString(")\n")
		}
	}

	return b.String()
}

func renderEntityLink(notePath, name string) string {
	if strings.TrimSpace(notePath) == "" {
		return name
	}
	return obsidianLink(notePath, name)
}

func firstNonEmptyEntityValue(values ...string) string {
	for _, value := range values {
		if trimmed := strings.TrimSpace(value); trimmed != "" {
			return trimmed
		}
	}
	return ""
}

func entityHeading(kind entities.Kind) string {
	raw := string(kind)
	if raw == "" {
		return "Entities"
	}
	runes := []rune(raw)
	runes[0] = unicode.ToUpper(runes[0])
	return string(runes)
}
