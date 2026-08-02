package okf

import (
	"bytes"
	"fmt"
	"strings"

	"gopkg.in/yaml.v3"
)

func RenderDocument(doc Document) (string, error) {
	if strings.TrimSpace(doc.Path) == "" {
		return "", fmt.Errorf("document path is required")
	}
	if strings.TrimSpace(doc.Type) == "" {
		return "", fmt.Errorf("document %s type is required", doc.Path)
	}
	fm, err := renderFrontmatter(doc)
	if err != nil {
		return "", err
	}
	body := strings.TrimRight(doc.Body, " \t\r\n")
	if body != "" {
		body += "\n"
	}
	return fm + "\n" + body, nil
}

func renderFrontmatter(doc Document) (string, error) {
	fields := []Field{
		{Name: "type", Value: strings.TrimSpace(doc.Type)},
	}
	if value := strings.TrimSpace(doc.Title); value != "" {
		fields = append(fields, Field{Name: "title", Value: value})
	}
	if value := strings.TrimSpace(doc.Description); value != "" {
		fields = append(fields, Field{Name: "description", Value: value})
	}
	if value := strings.TrimSpace(doc.Resource); value != "" {
		fields = append(fields, Field{Name: "resource", Value: value})
	}
	if len(doc.Tags) > 0 {
		fields = append(fields, Field{Name: "tags", Value: doc.Tags})
	}
	if strings.TrimSpace(doc.Generated.By) != "" {
		fields = append(fields, Field{Name: "generated", Value: doc.Generated})
	}
	if len(doc.Sources) > 0 {
		fields = append(fields, Field{Name: "sources", Value: doc.Sources})
	}
	fields = append(fields, doc.Fields...)

	node := &yaml.Node{Kind: yaml.MappingNode}
	for _, field := range fields {
		name := strings.TrimSpace(field.Name)
		if name == "" || shouldSkipFrontmatterValue(field.Value) {
			continue
		}
		valueNode, err := yamlNodeForValue(field.Value)
		if err != nil {
			return "", fmt.Errorf("frontmatter %s.%s: %w", doc.Path, name, err)
		}
		node.Content = append(node.Content, &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: name}, valueNode)
	}

	var buf bytes.Buffer
	buf.WriteString("---\n")
	encoder := yaml.NewEncoder(&buf)
	encoder.SetIndent(2)
	if err := encoder.Encode(node); err != nil {
		_ = encoder.Close()
		return "", fmt.Errorf("encode frontmatter %s: %w", doc.Path, err)
	}
	if err := encoder.Close(); err != nil {
		return "", fmt.Errorf("close frontmatter encoder %s: %w", doc.Path, err)
	}
	out := strings.TrimSuffix(buf.String(), "...\n")
	out = strings.TrimRight(out, "\n")
	out += "\n---\n"
	return out, nil
}

func yamlNodeForValue(value any) (*yaml.Node, error) {
	switch v := value.(type) {
	case string:
		return &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: strings.TrimSpace(v)}, nil
	case bool:
		if v {
			return &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!bool", Value: "true"}, nil
		}
		return &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!bool", Value: "false"}, nil
	case int:
		return &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!int", Value: fmt.Sprintf("%d", v)}, nil
	case int64:
		return &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!int", Value: fmt.Sprintf("%d", v)}, nil
	case []string:
		node := &yaml.Node{Kind: yaml.SequenceNode}
		for _, entry := range v {
			entry = strings.TrimSpace(entry)
			if entry == "" {
				continue
			}
			node.Content = append(node.Content, &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: entry})
		}
		return node, nil
	case Generated:
		node := &yaml.Node{Kind: yaml.MappingNode}
		appendStringMapping(node, "by", v.By)
		appendStringMapping(node, "at", v.At)
		return node, nil
	case []SourceReference:
		node := &yaml.Node{Kind: yaml.SequenceNode}
		for _, source := range v {
			if strings.TrimSpace(source.Resource) == "" {
				continue
			}
			entry := &yaml.Node{Kind: yaml.MappingNode}
			appendStringMapping(entry, "resource", source.Resource)
			appendStringMapping(entry, "title", source.Title)
			node.Content = append(node.Content, entry)
		}
		return node, nil
	default:
		return nil, fmt.Errorf("unsupported value type %T", value)
	}
}

func appendStringMapping(node *yaml.Node, name, value string) {
	value = strings.TrimSpace(value)
	if value == "" {
		return
	}
	node.Content = append(node.Content,
		&yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: name},
		&yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: value},
	)
}

func shouldSkipFrontmatterValue(value any) bool {
	if value == nil {
		return true
	}
	switch v := value.(type) {
	case string:
		return strings.TrimSpace(v) == ""
	case []string:
		for _, entry := range v {
			if strings.TrimSpace(entry) != "" {
				return false
			}
		}
		return true
	default:
		return false
	}
}
