package okf

import (
	"path"
	"sort"
	"strings"
)

func indexDocuments(docs []Document) []Document {
	byDir := map[string][]Document{}
	dirs := map[string]struct{}{"": {}}
	for _, doc := range docs {
		if path.Base(doc.Path) == "index.md" {
			continue
		}
		dir := path.Dir(doc.Path)
		if dir == "." {
			dir = ""
		}
		byDir[dir] = append(byDir[dir], doc)
		for parent := dir; ; {
			if parent == "." {
				parent = ""
			}
			dirs[parent] = struct{}{}
			if parent == "" {
				break
			}
			parent = path.Dir(parent)
		}
	}

	dirList := make([]string, 0, len(dirs))
	for dir := range dirs {
		dirList = append(dirList, dir)
	}
	sort.Strings(dirList)

	indexes := make([]Document, 0, len(dirList))
	for _, dir := range dirList {
		entries := entriesForIndexDir(dir, docs)
		body := renderIndexBody(dir, entries)
		indexPath := "index.md"
		if dir != "" {
			indexPath = path.Join(dir, "index.md")
		}
		indexes = append(indexes, Document{Path: indexPath, Body: body})
	}
	return indexes
}

func entriesForIndexDir(dir string, docs []Document) []Document {
	var entries []Document
	prefix := ""
	if dir != "" {
		prefix = dir + "/"
	}
	for _, doc := range docs {
		if path.Base(doc.Path) == "index.md" {
			continue
		}
		if dir == "" {
			entries = append(entries, doc)
			continue
		}
		if strings.HasPrefix(doc.Path, prefix) {
			entries = append(entries, doc)
		}
	}
	sort.Slice(entries, func(i, j int) bool {
		if entries[i].Type == entries[j].Type {
			if entries[i].Title == entries[j].Title {
				return entries[i].Path < entries[j].Path
			}
			return entries[i].Title < entries[j].Title
		}
		return entries[i].Type < entries[j].Type
	})
	return entries
}

func renderIndexBody(dir string, entries []Document) string {
	title := "Index"
	if dir != "" {
		title = path.Base(dir) + " Index"
	}
	var b strings.Builder
	b.WriteString("# ")
	b.WriteString(title)
	b.WriteString("\n")
	if len(entries) == 0 {
		b.WriteString("\nNo concepts in this directory.\n")
		return b.String()
	}

	currentType := ""
	indexPath := "index.md"
	if dir != "" {
		indexPath = path.Join(dir, "index.md")
	}
	for _, entry := range entries {
		if entry.Type != currentType {
			currentType = entry.Type
			b.WriteString("\n## ")
			b.WriteString(currentType)
			b.WriteString("\n\n")
		}
		rel, err := RelativeLink(indexPath, entry.Path)
		if err != nil {
			rel = entry.Path
		}
		b.WriteString("- ")
		b.WriteString(MarkdownLink(firstNonEmpty(entry.Title, entry.Path), rel))
		if strings.TrimSpace(entry.Description) != "" {
			b.WriteString(" - ")
			b.WriteString(cleanText(entry.Description))
		}
		b.WriteString("\n")
	}
	return b.String()
}
