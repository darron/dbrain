package install

import (
	"bytes"
	"fmt"
	"strconv"
	"strings"

	"gopkg.in/yaml.v3"
)

func buildConfig(template []byte, selections Selections, tools []Tool, secretRefs map[SecretKind]string) ([]byte, error) {
	if len(bytes.TrimSpace(template)) == 0 {
		return nil, fmt.Errorf("config template is empty")
	}

	var doc yaml.Node
	if err := yaml.Unmarshal(template, &doc); err != nil {
		return nil, fmt.Errorf("parse config template: %w", err)
	}
	root := documentRoot(&doc)
	if root == nil {
		return nil, fmt.Errorf("config template has no document root")
	}
	if root.Kind != yaml.MappingNode {
		return nil, fmt.Errorf("config template root must be a YAML mapping")
	}

	setBool(root, []string{"apple_notes", "enabled"}, selections.EnableAppleNotes)
	setBool(root, []string{"safari_tabs", "enabled"}, selections.EnableSafariTabs)
	setBool(root, []string{"scheduler", "sync_all", "enabled"}, selections.EnableScheduler)
	setBool(root, []string{"scheduler", "sync_all", "apple_notes"}, selections.EnableAppleNotes)
	setBool(root, []string{"scheduler", "sync_all", "safari_tabs"}, selections.EnableSafariTabs)
	setBool(root, []string{"scheduler", "sync_all", "skip_x_photo_ocr"}, selections.SkipXPhotoOCR)
	setBool(root, []string{"scheduler", "sync_all", "skip_categorize"}, selections.SkipCategorize)
	if selections.EnableTailscale {
		setBool(root, []string{"tsnet", "web"}, true)
		setBool(root, []string{"tsnet", "mcp"}, true)
		if value := strings.TrimSpace(selections.TSNetHostname); value != "" {
			setString(root, []string{"tsnet", "hostname"}, value)
		}
	}
	if selections.EnableGitHubLogin {
		setBool(root, []string{"auth", "enabled"}, true)
		setStringSequence(root, []string{"auth", "providers"}, []string{"github"})
		if value := strings.TrimSpace(selections.AuthBaseURL); value != "" {
			setString(root, []string{"auth", "base_url"}, value)
		}
		if value := strings.TrimSpace(selections.GitHubClientID); value != "" {
			setString(root, []string{"auth", "github", "client_id"}, value)
		}
	}

	if value := strings.TrimSpace(selections.SummaryModel); value != "" {
		setString(root, []string{"summary", "model"}, value)
	}
	if value := strings.TrimSpace(selections.CategorizeModel); value != "" {
		setString(root, []string{"categorize", "model"}, value)
	}
	if value := strings.TrimSpace(selections.OCRModel); value != "" {
		setString(root, []string{"ocr", "model"}, value)
	}
	if tesseract := firstAvailableToolPath(tools, ToolTesseract); tesseract != "" {
		setString(root, []string{"apple_notes", "tesseract_binary"}, tesseract)
	}
	for _, spec := range secretSpecs {
		if ref := strings.TrimSpace(secretRefs[spec.Kind]); ref != "" {
			setString(root, spec.Path, ref)
		}
	}

	var out bytes.Buffer
	encoder := yaml.NewEncoder(&out)
	encoder.SetIndent(2)
	if err := encoder.Encode(&doc); err != nil {
		return nil, fmt.Errorf("render config: %w", err)
	}
	if err := encoder.Close(); err != nil {
		return nil, fmt.Errorf("close config encoder: %w", err)
	}
	return out.Bytes(), nil
}

func documentRoot(doc *yaml.Node) *yaml.Node {
	if doc == nil {
		return nil
	}
	if doc.Kind == yaml.DocumentNode && len(doc.Content) > 0 {
		return doc.Content[0]
	}
	return doc
}

func setString(root *yaml.Node, path []string, value string) {
	setScalar(root, path, "!!str", value)
}

func setBool(root *yaml.Node, path []string, value bool) {
	setScalar(root, path, "!!bool", strconv.FormatBool(value))
}

func setStringSequence(root *yaml.Node, path []string, values []string) {
	if len(path) == 0 || root == nil {
		return
	}
	node := ensurePath(root, path[:len(path)-1])
	if node == nil || node.Kind != yaml.MappingNode {
		return
	}
	seq := &yaml.Node{Kind: yaml.SequenceNode, Tag: "!!seq"}
	for _, value := range values {
		if trimmed := strings.TrimSpace(value); trimmed != "" {
			seq.Content = append(seq.Content, &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: trimmed})
		}
	}
	key := path[len(path)-1]
	if existing := mappingValue(node, key); existing != nil {
		*existing = *seq
		return
	}
	node.Content = append(node.Content,
		&yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: key},
		seq,
	)
}

func setScalar(root *yaml.Node, path []string, tag string, value string) {
	if len(path) == 0 || root == nil {
		return
	}
	node := ensurePath(root, path[:len(path)-1])
	if node == nil || node.Kind != yaml.MappingNode {
		return
	}
	key := path[len(path)-1]
	if existing := mappingValue(node, key); existing != nil {
		existing.Kind = yaml.ScalarNode
		existing.Tag = tag
		existing.Value = value
		existing.Content = nil
		return
	}
	node.Content = append(node.Content,
		&yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: key},
		&yaml.Node{Kind: yaml.ScalarNode, Tag: tag, Value: value},
	)
}

func ensurePath(root *yaml.Node, path []string) *yaml.Node {
	current := root
	for _, part := range path {
		next := mappingValue(current, part)
		if next == nil {
			next = &yaml.Node{Kind: yaml.MappingNode, Tag: "!!map"}
			current.Content = append(current.Content,
				&yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: part},
				next,
			)
		}
		if next.Kind != yaml.MappingNode {
			next.Kind = yaml.MappingNode
			next.Tag = "!!map"
			next.Value = ""
			next.Content = nil
		}
		current = next
	}
	return current
}

func mappingValue(node *yaml.Node, key string) *yaml.Node {
	if node == nil || node.Kind != yaml.MappingNode {
		return nil
	}
	for i := 0; i+1 < len(node.Content); i += 2 {
		if strings.EqualFold(node.Content[i].Value, key) {
			return node.Content[i+1]
		}
	}
	return nil
}

func firstAvailableToolPath(tools []Tool, id ToolID) string {
	for _, tool := range tools {
		if tool.ID == id && tool.Available && strings.TrimSpace(tool.Path) != "" {
			return strings.TrimSpace(tool.Path)
		}
	}
	return ""
}
