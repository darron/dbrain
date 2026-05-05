package xmediatranscribe

import (
	"crypto/sha256"
	"encoding/hex"
	"strings"

	"github.com/darron/dbrain/internal/model"
	"github.com/darron/dbrain/internal/xpost"
)

func buildTranscriptSummaryInput(item model.Item) string {
	var b strings.Builder
	b.WriteString("X post context:\n")
	if snapshot, ok, _ := xpost.DecodeSnapshot(item.XPostJSON); ok && snapshot != nil {
		writeSnapshotSummaryContext(&b, snapshot, "Primary post", strings.TrimSpace(item.XPostText))
	} else if text := strings.TrimSpace(item.XPostText); text != "" {
		b.WriteString(text)
	} else {
		b.WriteString("(none)")
	}
	b.WriteString("\n\nVideo transcript:\n")
	b.WriteString(strings.TrimSpace(item.ArticleText))
	return b.String()
}

func writeSnapshotSummaryContext(b *strings.Builder, snapshot *xpost.Snapshot, label string, fallbackText string) {
	if snapshot == nil {
		b.WriteString("(none)")
		return
	}
	b.WriteString(label)
	b.WriteString(":\n")
	if snapshot.AuthorHandle != "" || snapshot.AuthorName != "" {
		b.WriteString("Author: ")
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
	if url := strings.TrimSpace(snapshot.URL); url != "" {
		b.WriteString("URL: ")
		b.WriteString(url)
		b.WriteString("\n")
	}
	if text := firstNonEmptyText(strings.TrimSpace(snapshot.Text), strings.TrimSpace(fallbackText)); text != "" {
		b.WriteString(text)
		b.WriteString("\n")
	} else {
		b.WriteString("(no text)\n")
	}
	if snapshot.QuotedPost != nil {
		b.WriteString("\n")
		writeSnapshotSummaryContext(b, snapshot.QuotedPost, "Quoted post", "")
	}
}

func firstNonEmptyText(values ...string) string {
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value != "" {
			return value
		}
	}
	return ""
}

func hashSummaryInput(value string) string {
	sum := sha256.Sum256([]byte(strings.TrimSpace(value)))
	return hex.EncodeToString(sum[:])
}
