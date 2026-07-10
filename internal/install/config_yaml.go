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

	setBool(root, []string{"sync_all", "imports", "x_bookmarks"}, selections.ImportXBookmarks)
	setBool(root, []string{"sync_all", "imports", "github_stars"}, selections.ImportGitHubStars)
	setBool(root, []string{"sync_all", "imports", "youtube_watch_later"}, selections.ImportYouTubeWatchLater)
	setBool(root, []string{"sync_all", "imports", "youtube_liked"}, selections.ImportYouTubeLiked)
	setBool(root, []string{"sync_all", "imports", "feeds"}, selections.ImportFeeds)
	setBool(root, []string{"sync_all", "imports", "apple_notes"}, selections.EnableAppleNotes)
	setBool(root, []string{"sync_all", "imports", "safari_tabs"}, selections.EnableSafariTabs)
	if value := strings.TrimSpace(selections.SyncBrowser); value != "" {
		setString(root, []string{"sync_all", "browser"}, value)
	}
	if value := strings.TrimSpace(selections.SyncProfile); value != "" {
		setString(root, []string{"sync_all", "profile"}, value)
	}
	setBool(root, []string{"apple_notes", "enabled"}, selections.EnableAppleNotes)
	setBool(root, []string{"safari_tabs", "enabled"}, selections.EnableSafariTabs)
	if value := strings.TrimSpace(selections.SafariTabsDevice); value != "" {
		setString(root, []string{"safari_tabs", "device"}, value)
	}
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

func configHasNonEmptyScalar(data []byte, path ...string) bool {
	if len(bytes.TrimSpace(data)) == 0 || len(path) == 0 {
		return false
	}
	var doc yaml.Node
	if err := yaml.Unmarshal(data, &doc); err != nil {
		return false
	}
	current := documentRoot(&doc)
	for _, part := range path {
		current = mappingValue(current, part)
		if current == nil {
			return false
		}
	}
	return current.Kind == yaml.ScalarNode && strings.TrimSpace(current.Value) != ""
}

// SeedSelectionsFromConfig restores the effective import and browser selections
// represented by an existing config before a reinstall applies explicit flags or
// presents the interactive checklist. Configs without sync_all.imports retain
// the legacy sync all defaults.
func SeedSelectionsFromConfig(selections Selections, data []byte) (Selections, error) {
	if len(bytes.TrimSpace(data)) == 0 {
		return selections, nil
	}

	var doc yaml.Node
	if err := yaml.Unmarshal(data, &doc); err != nil {
		return selections, fmt.Errorf("parse existing config: %w", err)
	}
	root := documentRoot(&doc)
	if root == nil || root.Kind != yaml.MappingNode {
		return selections, fmt.Errorf("existing config root must be a YAML mapping")
	}

	selections.ImportXBookmarks = true
	selections.ImportGitHubStars = true
	selections.ImportYouTubeWatchLater = true
	selections.ImportYouTubeLiked = true
	selections.ImportFeeds = true

	boolFields := []struct {
		path   []string
		target *bool
	}{
		{path: []string{"apple_notes", "enabled"}, target: &selections.EnableAppleNotes},
		{path: []string{"safari_tabs", "enabled"}, target: &selections.EnableSafariTabs},
		{path: []string{"scheduler", "sync_all", "apple_notes"}, target: &selections.EnableAppleNotes},
		{path: []string{"scheduler", "sync_all", "safari_tabs"}, target: &selections.EnableSafariTabs},
		{path: []string{"sync_all", "imports", "x_bookmarks"}, target: &selections.ImportXBookmarks},
		{path: []string{"sync_all", "imports", "github_stars"}, target: &selections.ImportGitHubStars},
		{path: []string{"sync_all", "imports", "youtube_watch_later"}, target: &selections.ImportYouTubeWatchLater},
		{path: []string{"sync_all", "imports", "youtube_liked"}, target: &selections.ImportYouTubeLiked},
		{path: []string{"sync_all", "imports", "feeds"}, target: &selections.ImportFeeds},
		{path: []string{"sync_all", "imports", "apple_notes"}, target: &selections.EnableAppleNotes},
		{path: []string{"sync_all", "imports", "safari_tabs"}, target: &selections.EnableSafariTabs},
		{path: []string{"scheduler", "sync_all", "enabled"}, target: &selections.EnableScheduler},
	}
	for _, field := range boolFields {
		value, ok, err := configBool(root, field.path...)
		if err != nil {
			return selections, err
		}
		if ok {
			*field.target = value
		}
	}

	if value, ok := configScalar(root, "safari_tabs", "device"); ok {
		selections.SafariTabsDevice = strings.TrimSpace(value)
	}
	if value, ok := configScalar(root, "sync_all", "browser"); ok {
		selections.SyncBrowser = strings.TrimSpace(value)
	}
	if value, ok := configScalar(root, "sync_all", "profile"); ok {
		selections.SyncProfile = strings.TrimSpace(value)
	}
	return selections, nil
}

func configBool(root *yaml.Node, path ...string) (bool, bool, error) {
	node := configNode(root, path...)
	if node == nil {
		return false, false, nil
	}
	if node.Kind != yaml.ScalarNode {
		return false, false, fmt.Errorf("existing config %s must be a boolean", strings.Join(path, "."))
	}
	value, err := strconv.ParseBool(strings.TrimSpace(node.Value))
	if err != nil {
		return false, false, fmt.Errorf("existing config %s must be a boolean: %w", strings.Join(path, "."), err)
	}
	return value, true, nil
}

func configScalar(root *yaml.Node, path ...string) (string, bool) {
	node := configNode(root, path...)
	if node == nil || node.Kind != yaml.ScalarNode {
		return "", false
	}
	return node.Value, true
}

func configNode(root *yaml.Node, path ...string) *yaml.Node {
	current := root
	for _, part := range path {
		current = mappingValue(current, part)
		if current == nil {
			return nil
		}
	}
	return current
}

func firstAvailableToolPath(tools []Tool, id ToolID) string {
	for _, tool := range tools {
		if tool.ID == id && tool.Available && strings.TrimSpace(tool.Path) != "" {
			return strings.TrimSpace(tool.Path)
		}
	}
	return ""
}
