package sourceenrich

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/darron/dbrain/internal/model"
)

func canSummarizeStoredExtract(source model.SourceDocument) bool {
	if source.ExtractStatus != model.SourceExtractStatusOK {
		return false
	}
	return strings.TrimSpace(source.ExtractedText) != ""
}

func skipSummaryReason(source model.SourceDocument, extract model.ExtractResult) (string, bool) {
	if reason, ok := genericSkipSummaryReason(extract); ok {
		return reason, true
	}
	if source.SourceType != "youtube" {
		return "", false
	}
	if strings.TrimSpace(extract.RawJSON) == "" {
		return "", false
	}

	var payload youtubeExtractEnvelope
	if err := json.Unmarshal([]byte(extract.RawJSON), &payload); err != nil {
		return "", false
	}

	transcriptSource := ""
	if payload.Extracted.TranscriptSource != nil {
		transcriptSource = strings.TrimSpace(*payload.Extracted.TranscriptSource)
	}
	transcriptionProvider := ""
	if payload.Extracted.TranscriptionProvider != nil {
		transcriptionProvider = strings.TrimSpace(*payload.Extracted.TranscriptionProvider)
	}
	transcriptChars := 0
	if payload.Extracted.TranscriptCharacters != nil {
		transcriptChars = *payload.Extracted.TranscriptCharacters
	}

	if transcriptChars > 0 || transcriptionProvider != "" {
		return "", false
	}
	if transcriptSource == "captionTracks" || transcriptSource == "youtubei" {
		return "", false
	}
	if transcriptSource == "unavailable" && len(strings.TrimSpace(extract.Content)) <= 200 {
		return "youtube transcript unavailable and no audio transcription was produced", true
	}
	return "", false
}

func genericSkipSummaryReason(extract model.ExtractResult) (string, bool) {
	content := strings.TrimSpace(extract.Content)
	if content == "" {
		return "", false
	}
	if reason, ok := mediaURLSkipSummaryReason(extract); ok {
		return reason, true
	}
	if looksLikeNonTextExtractContent(content) {
		return "extracted content appears to be binary/non-text; text summarization skipped", true
	}
	if looksLikePlaceholderExtractContent(content) {
		return "extracted content appears to be redirect/login/placeholder boilerplate rather than substantive content", true
	}
	if reason, ok := waybackSkipSummaryReason(extract); ok {
		return reason, true
	}
	return "", false
}

const maxLowSignalWaybackExtractChars = 500

func waybackSkipSummaryReason(extract model.ExtractResult) (string, bool) {
	if strings.TrimSpace(extract.Tool) != waybackToolName {
		return "", false
	}
	content := strings.TrimSpace(extract.Content)
	if content == "" {
		return "", false
	}
	if len(content) < maxLowSignalWaybackExtractChars {
		return fmt.Sprintf("wayback extract is too short to summarize reliably (%d chars)", len(content)), true
	}
	return "", false
}

func blockedSummaryReason(err error) (string, bool) {
	if err == nil {
		return "", false
	}
	value := strings.ToLower(strings.TrimSpace(err.Error()))
	switch {
	case strings.Contains(value, "maximum context length"),
		strings.Contains(value, "context length"),
		strings.Contains(value, "too many tokens"),
		strings.Contains(value, "input is too long"),
		strings.Contains(value, "context deadline exceeded"),
		strings.Contains(value, "timeout"),
		strings.Contains(value, "timed out"),
		strings.Contains(value, "signal: killed"):
		return err.Error(), true
	default:
		return "", false
	}
}
