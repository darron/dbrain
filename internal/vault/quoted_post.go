package vault

import (
	"strings"

	"github.com/darron/dbrain/internal/xpost"
)

func writeQuotedPostSection(b *strings.Builder, snapshot *xpost.Snapshot) {
	if snapshot == nil {
		return
	}

	b.WriteString("\n## Quoted X Post\n\n")
	if notePath := xpost.NotePath(snapshot); notePath != "" {
		b.WriteString("- Linked item: [[")
		b.WriteString(notePath)
		b.WriteString("]]\n")
	}
	if url := strings.TrimSpace(snapshot.URL); url != "" {
		b.WriteString("- URL: ")
		b.WriteString(url)
		b.WriteString("\n")
	}
	if snapshot.AuthorHandle != "" || snapshot.AuthorName != "" {
		b.WriteString("- Author: ")
		if snapshot.AuthorName != "" {
			b.WriteString(snapshot.AuthorName)
			if snapshot.AuthorHandle != "" {
				b.WriteString(" ")
			}
		}
		if snapshot.AuthorHandle != "" {
			b.WriteString("(@")
			b.WriteString(snapshot.AuthorHandle)
			b.WriteString(")")
		}
		b.WriteString("\n")
	}
	if postedAt := strings.TrimSpace(snapshot.PostedAt); postedAt != "" {
		b.WriteString("- Published: ")
		b.WriteString(postedAt)
		b.WriteString("\n")
	}
	if len(snapshot.Links) > 0 {
		b.WriteString("- Links:\n")
		for _, link := range snapshot.Links {
			link = strings.TrimSpace(link)
			if link == "" {
				continue
			}
			b.WriteString("  - ")
			b.WriteString(link)
			b.WriteString("\n")
		}
	}
	if text := strings.TrimSpace(snapshot.Text); text != "" {
		b.WriteString("\n")
		b.WriteString(text)
		b.WriteString("\n")
	}
}
