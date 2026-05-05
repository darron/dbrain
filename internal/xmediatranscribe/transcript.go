package xmediatranscribe

import (
	"strings"

	"github.com/darron/dbrain/internal/model"
)

func shouldSkipTranscript(transcript string) bool {
	body := strings.TrimSpace(transcript)
	if body == "" {
		return true
	}
	if _, ok := transcriptNoiseMarkers[strings.ToLower(body)]; ok {
		return true
	}
	return len(body) <= 40
}

func classifyTranscriptSkip(transcript string) string {
	body := strings.TrimSpace(transcript)
	if body == "" {
		return model.XMediaTranscriptStatusEmpty
	}
	if _, ok := transcriptNoiseMarkers[strings.ToLower(body)]; ok {
		return model.XMediaTranscriptStatusNoise
	}
	return model.XMediaTranscriptStatusTooShort
}

func classifyTranscriptError(err error) string {
	if err == nil {
		return ""
	}
	if strings.Contains(strings.ToLower(err.Error()), "transcript empty") {
		return model.XMediaTranscriptStatusEmpty
	}
	return model.XMediaTranscriptStatusError
}

func chooseItemTranscriptOutcome(current itemTranscriptOutcome, next itemTranscriptOutcome) itemTranscriptOutcome {
	if outcomePriority(next.Status) >= outcomePriority(current.Status) {
		return next
	}
	return current
}

func outcomePriority(status string) int {
	switch status {
	case model.XMediaTranscriptStatusError:
		return 5
	case model.XMediaTranscriptStatusEmpty:
		return 4
	case model.XMediaTranscriptStatusNoise:
		return 3
	case model.XMediaTranscriptStatusTooShort:
		return 2
	case model.XMediaTranscriptStatusNoAudio:
		return 1
	default:
		return 0
	}
}

func renderTranscriptBlocks(blocks []transcriptBlock) string {
	var b strings.Builder
	for i, block := range blocks {
		if i > 0 {
			b.WriteString("\n\n")
		}
		b.WriteString("### ")
		b.WriteString(block.Heading)
		b.WriteString("\n\n")
		if block.RemoteURL != "" {
			b.WriteString("Remote URL: ")
			b.WriteString(block.RemoteURL)
			b.WriteString("\n")
		}
		if block.ExpandedURL != "" {
			b.WriteString("Post Media URL: ")
			b.WriteString(block.ExpandedURL)
			b.WriteString("\n")
		}
		if block.LocalPath != "" {
			b.WriteString("Local Path: `")
			b.WriteString(block.LocalPath)
			b.WriteString("`\n")
		}
		b.WriteString("\nTranscript:\n\n")
		b.WriteString(strings.TrimSpace(block.Text))
	}
	return strings.TrimSpace(b.String())
}
