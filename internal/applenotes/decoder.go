package applenotes

import (
	"bytes"
	"compress/gzip"
	"compress/zlib"
	"encoding/binary"
	"fmt"
	"io"
	"strings"
	"unicode"
	"unicode/utf8"
)

func DecodeZData(data []byte) (string, error) {
	if len(bytes.TrimSpace(data)) == 0 {
		return "", nil
	}

	payload := data
	if decoded, ok := decompressZData(data); ok {
		payload = decoded
	}
	if text, ok := printableText(payload); ok {
		return text, nil
	}

	stringsFound := extractProtoStrings(payload, 0)
	if len(stringsFound) == 0 {
		stringsFound = extractPrintableRuns(payload)
	}
	if len(stringsFound) == 0 {
		return "", fmt.Errorf("decode Apple Notes ZDATA: no printable body text found")
	}
	return strings.Join(dedupeStrings(stringsFound), "\n\n"), nil
}

func decompressZData(data []byte) ([]byte, bool) {
	if len(data) >= 2 && data[0] == 0x1f && data[1] == 0x8b {
		reader, err := gzip.NewReader(bytes.NewReader(data))
		if err == nil {
			defer func() {
				_ = reader.Close()
			}()
			if decoded, readErr := io.ReadAll(reader); readErr == nil {
				return decoded, true
			}
		}
	}

	reader, err := zlib.NewReader(bytes.NewReader(data))
	if err == nil {
		defer func() {
			_ = reader.Close()
		}()
		if decoded, readErr := io.ReadAll(reader); readErr == nil {
			return decoded, true
		}
	}

	return nil, false
}

func printableText(data []byte) (string, bool) {
	text := strings.TrimSpace(string(data))
	if text == "" || !utf8.ValidString(text) {
		return "", false
	}
	runes := []rune(text)
	if len(runes) < 3 {
		return "", false
	}
	printable := 0
	for _, r := range runes {
		if r == '\n' || r == '\r' || r == '\t' || unicode.IsPrint(r) {
			printable++
			continue
		}
		if r < 0x20 {
			return "", false
		}
	}
	if float64(printable)/float64(len(runes)) < 0.85 {
		return "", false
	}
	return normalizeDecodedText(text), true
}

func extractProtoStrings(data []byte, depth int) []string {
	if depth > 6 || len(data) == 0 {
		return nil
	}
	var found []string
	for offset := 0; offset < len(data); {
		key, n := binary.Uvarint(data[offset:])
		if n <= 0 {
			break
		}
		offset += n
		wireType := key & 0x7
		switch wireType {
		case 0:
			_, n := binary.Uvarint(data[offset:])
			if n <= 0 {
				return found
			}
			offset += n
		case 1:
			if offset+8 > len(data) {
				return found
			}
			offset += 8
		case 2:
			length, n := binary.Uvarint(data[offset:])
			if n <= 0 {
				return found
			}
			offset += n
			if length > uint64(len(data)-offset) {
				return found
			}
			segment := data[offset : offset+int(length)]
			offset += int(length)
			if text, ok := printableText(segment); ok {
				found = append(found, text)
			}
			if nested := extractProtoStrings(segment, depth+1); len(nested) > 0 {
				found = append(found, nested...)
			}
		case 5:
			if offset+4 > len(data) {
				return found
			}
			offset += 4
		default:
			return found
		}
	}
	return found
}

func extractPrintableRuns(data []byte) []string {
	var found []string
	var b strings.Builder
	flush := func() {
		text := normalizeDecodedText(b.String())
		b.Reset()
		if len([]rune(text)) >= 4 {
			found = append(found, text)
		}
	}
	for len(data) > 0 {
		r, size := utf8.DecodeRune(data)
		if r == utf8.RuneError && size == 1 {
			flush()
			data = data[1:]
			continue
		}
		if r == '\n' || r == '\r' || r == '\t' || unicode.IsPrint(r) {
			b.WriteRune(r)
		} else {
			flush()
		}
		data = data[size:]
	}
	flush()
	return found
}

func dedupeStrings(values []string) []string {
	seen := map[string]struct{}{}
	deduped := make([]string, 0, len(values))
	for _, value := range values {
		value = normalizeDecodedText(value)
		if value == "" {
			continue
		}
		key := strings.ToLower(value)
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		deduped = append(deduped, value)
	}
	return deduped
}

func normalizeDecodedText(value string) string {
	lines := strings.Split(strings.ReplaceAll(value, "\r\n", "\n"), "\n")
	for i := range lines {
		lines[i] = strings.TrimSpace(lines[i])
	}
	return strings.TrimSpace(strings.Join(lines, "\n"))
}
