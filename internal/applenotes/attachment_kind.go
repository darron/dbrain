package applenotes

import (
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
)

func classifyAttachmentKind(attachment Attachment, localPath string) string {
	mimeType := strings.ToLower(strings.TrimSpace(attachment.MIMEType))
	uti := strings.ToLower(strings.TrimSpace(attachment.UTI))
	ext := strings.ToLower(filepath.Ext(firstNonEmpty(attachment.FileName, localPath, attachment.FilePath)))
	if strings.Contains(mimeType, "pdf") || strings.Contains(uti, "pdf") {
		return "pdf"
	}
	if strings.HasPrefix(mimeType, "image/") || strings.Contains(uti, "image") {
		return "image"
	}
	if strings.HasPrefix(mimeType, "text/") || strings.Contains(uti, "plain-text") || strings.Contains(uti, "html") {
		return "text"
	}
	if ext == ".pdf" {
		return "pdf"
	}
	if isImageExt(ext) {
		return "image"
	}
	if isTextExt(ext) {
		return "text"
	}
	if detected := detectFileKind(localPath); detected != "" {
		return detected
	}
	return ""
}

func detectFileKind(path string) string {
	f, err := os.Open(path)
	if err != nil {
		return ""
	}
	defer func() {
		_ = f.Close()
	}()
	buf := make([]byte, 512)
	n, _ := io.ReadFull(f, buf)
	if n == 0 {
		return ""
	}
	sniffed := strings.ToLower(http.DetectContentType(buf[:n]))
	switch {
	case strings.Contains(sniffed, "pdf"):
		return "pdf"
	case strings.HasPrefix(sniffed, "image/"):
		return "image"
	case strings.HasPrefix(sniffed, "text/"):
		return "text"
	default:
		return ""
	}
}

func isImageExt(ext string) bool {
	switch ext {
	case ".png", ".jpg", ".jpeg", ".gif", ".webp", ".tif", ".tiff", ".bmp", ".heic", ".heif":
		return true
	default:
		return false
	}
}

func isTextExt(ext string) bool {
	switch ext {
	case ".txt", ".md", ".markdown", ".csv", ".tsv", ".json", ".jsonl", ".yaml", ".yml", ".xml", ".html", ".htm", ".log", ".text":
		return true
	default:
		return false
	}
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return strings.TrimSpace(value)
		}
	}
	return ""
}
