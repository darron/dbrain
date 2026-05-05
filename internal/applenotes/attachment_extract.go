package applenotes

import (
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/ledongthuc/pdf"
)

func extractPlainTextFile(path string, maxBytes int64) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer func() {
		_ = f.Close()
	}()
	limit := maxBytes
	if limit <= 0 {
		limit = defaultAttachmentMaxBytes
	}
	return readLimitedText(f, limit)
}

func extractPDFText(path string) (string, error) {
	f, reader, err := pdf.Open(path)
	if err != nil {
		return "", err
	}
	defer func() {
		_ = f.Close()
	}()
	textReader, err := reader.GetPlainText()
	if err != nil {
		return "", err
	}
	return readLimitedText(textReader, defaultAttachmentMaxBytes)
}

func readLimitedText(reader io.Reader, limit int64) (string, error) {
	if limit <= 0 {
		limit = defaultAttachmentMaxBytes
	}
	var b strings.Builder
	if _, err := io.Copy(&b, io.LimitReader(reader, limit+1)); err != nil {
		return "", err
	}
	if int64(b.Len()) > limit {
		return "", fmt.Errorf("%w: extracted text exceeds %d bytes", errAttachmentTooLarge, limit)
	}
	return strings.TrimSpace(b.String()), nil
}
