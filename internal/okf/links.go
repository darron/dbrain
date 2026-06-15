package okf

import (
	"fmt"
	"net/url"
	"path"
	"path/filepath"
	"strings"
)

func RelativeLink(fromPath, toPath string) (string, error) {
	fromDir := path.Dir(path.Clean(fromPath))
	rel, err := filepath.Rel(filepath.FromSlash(fromDir), filepath.FromSlash(path.Clean(toPath)))
	if err != nil {
		return "", fmt.Errorf("relative link %s -> %s: %w", fromPath, toPath, err)
	}
	rel = filepath.ToSlash(rel)
	if rel == "." {
		return path.Base(toPath), nil
	}
	return rel, nil
}

func MarkdownLink(label, dest string) string {
	label = escapeMarkdownLinkText(label)
	dest = strings.TrimSpace(dest)
	if dest == "" {
		return label
	}
	return "[" + label + "](<" + escapeMarkdownLinkDestination(dest) + ">)"
}

func escapeMarkdownLinkText(value string) string {
	value = strings.Join(strings.Fields(strings.TrimSpace(value)), " ")
	replacer := strings.NewReplacer(`\`, `\\`, `[`, `\[`, `]`, `\]`)
	return replacer.Replace(value)
}

func escapeMarkdownLinkDestination(value string) string {
	value = strings.ReplaceAll(value, "\n", "")
	value = strings.ReplaceAll(value, "\r", "")
	value = strings.ReplaceAll(value, ">", "%3E")
	return value
}

func isExternalDestination(dest string) bool {
	dest = strings.TrimSpace(dest)
	if dest == "" || strings.HasPrefix(dest, "#") {
		return true
	}
	if strings.HasPrefix(dest, "<") && strings.HasSuffix(dest, ">") {
		dest = strings.TrimSuffix(strings.TrimPrefix(dest, "<"), ">")
	}
	parsed, err := url.Parse(dest)
	if err == nil && parsed.Scheme != "" {
		return true
	}
	return strings.HasPrefix(dest, "mailto:")
}

func stripLinkFragment(dest string) string {
	if i := strings.Index(dest, "#"); i >= 0 {
		return dest[:i]
	}
	return dest
}
