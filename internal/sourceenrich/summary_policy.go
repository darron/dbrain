package sourceenrich

import (
	"encoding/json"
	"fmt"
	neturl "net/url"
	"path/filepath"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/darron/dbrain/internal/model"
)

func canSummarizeStoredExtract(source model.SourceDocument) bool {
	if source.ExtractStatus != "ok" {
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

func mediaURLSkipSummaryReason(extract model.ExtractResult) (string, bool) {
	rawURL := firstNonEmpty(extract.FinalURL, extract.CanonicalURL)
	if strings.TrimSpace(rawURL) == "" {
		return "", false
	}
	ext := sourceURLPathExtension(rawURL)
	if ext == "" {
		return "", false
	}
	if !isUnsupportedTextSummaryMediaExtension(ext) {
		return "", false
	}
	return fmt.Sprintf("source URL points to %s content (%s); text summarization skipped", unsupportedTextSummaryMediaKind(ext), ext), true
}

func sourceURLPathExtension(rawURL string) string {
	parsed, err := neturl.Parse(strings.TrimSpace(rawURL))
	if err != nil {
		return ""
	}
	path := parsed.EscapedPath()
	if path == "" {
		return ""
	}
	return strings.ToLower(filepath.Ext(path))
}

func isUnsupportedTextSummaryMediaExtension(ext string) bool {
	switch strings.ToLower(strings.TrimSpace(ext)) {
	case ".jpg", ".jpeg", ".png", ".gif", ".webp", ".avif", ".heic", ".heif", ".bmp", ".tif", ".tiff", ".ico", ".svg":
		return true
	case ".mp4", ".m4v", ".mov", ".webm", ".mkv", ".avi", ".mpeg", ".mpg":
		return true
	case ".mp3", ".m4a", ".aac", ".wav", ".flac", ".ogg", ".opus":
		return true
	case ".zip", ".tar", ".gz", ".tgz", ".bz2", ".xz", ".7z", ".rar", ".dmg", ".pkg":
		return true
	default:
		return false
	}
}

func unsupportedTextSummaryMediaKind(ext string) string {
	switch strings.ToLower(strings.TrimSpace(ext)) {
	case ".jpg", ".jpeg", ".png", ".gif", ".webp", ".avif", ".heic", ".heif", ".bmp", ".tif", ".tiff", ".ico", ".svg":
		return "image/media"
	case ".mp4", ".m4v", ".mov", ".webm", ".mkv", ".avi", ".mpeg", ".mpg":
		return "video/media"
	case ".mp3", ".m4a", ".aac", ".wav", ".flac", ".ogg", ".opus":
		return "audio/media"
	default:
		return "binary/media"
	}
}

func looksLikeNonTextExtractContent(content string) bool {
	content = strings.TrimSpace(content)
	if content == "" {
		return false
	}
	if strings.ContainsRune(content, '\x00') {
		return true
	}
	if !utf8.ValidString(content) {
		return true
	}

	runes := 0
	replacementRunes := 0
	controlRunes := 0
	for _, r := range content {
		runes++
		if r == utf8.RuneError {
			replacementRunes++
			continue
		}
		if unicode.IsControl(r) && r != '\n' && r != '\r' && r != '\t' {
			controlRunes++
		}
	}
	if runes == 0 {
		return false
	}
	if replacementRunes >= 3 && replacementRunes*20 >= runes {
		return true
	}
	return controlRunes >= 8 && controlRunes*10 >= runes
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

func looksLikePlaceholderExtractContent(content string) bool {
	normalized := strings.ToLower(strings.Join(strings.Fields(strings.TrimSpace(content)), " "))
	if normalized == "" {
		return false
	}
	switch {
	case strings.Contains(normalized, "redirecting"),
		strings.Contains(normalized, "you will be redirected"),
		strings.Contains(normalized, "if you are not redirected automatically"),
		strings.Contains(normalized, "loading..."),
		strings.Contains(normalized, "coming soon"),
		strings.Contains(normalized, "<div></div>"),
		strings.Contains(normalized, "we use cookies to improve user experience"),
		strings.Contains(normalized, "nothing to see here"),
		strings.Contains(normalized, "google drive"),
		strings.Contains(normalized, "your browser does not support frames"),
		strings.Contains(normalized, "click here to enter the site"):
		return len(normalized) <= 300
	case strings.Contains(normalized, "sign in or sign up"),
		strings.Contains(normalized, "you are not logged in"),
		strings.Contains(normalized, "manage account"),
		strings.Contains(normalized, "your profile"),
		strings.Contains(normalized, "continue with google"),
		strings.Contains(normalized, "continue with github"),
		strings.Contains(normalized, "open full screen to view more"),
		strings.Contains(normalized, "google apps"):
		return len(normalized) <= 300
	default:
		return false
	}
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
