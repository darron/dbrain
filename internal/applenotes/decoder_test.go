package applenotes

import (
	"bytes"
	"compress/gzip"
	"compress/zlib"
	"testing"
)

func TestDecodeZDataPlainText(t *testing.T) {
	t.Parallel()

	got, err := DecodeZData([]byte("plain note body"))
	if err != nil {
		t.Fatalf("DecodeZData: %v", err)
	}
	if got != "plain note body" {
		t.Fatalf("decoded = %q", got)
	}
}

func TestDecodeZDataGzipProtoStrings(t *testing.T) {
	t.Parallel()

	payload := []byte{0x0a, 0x05}
	payload = append(payload, []byte("Hello")...)
	payload = append(payload, 0x12, 0x05)
	payload = append(payload, []byte("World")...)

	var compressed bytes.Buffer
	writer := gzip.NewWriter(&compressed)
	if _, err := writer.Write(payload); err != nil {
		t.Fatalf("gzip write: %v", err)
	}
	if err := writer.Close(); err != nil {
		t.Fatalf("gzip close: %v", err)
	}

	got, err := DecodeZData(compressed.Bytes())
	if err != nil {
		t.Fatalf("DecodeZData: %v", err)
	}
	if got != "Hello\n\nWorld" {
		t.Fatalf("decoded = %q", got)
	}
}

func TestDecodeZDataProtoStrings(t *testing.T) {
	t.Parallel()

	linkAndTag := []byte("https://example.com/page #Research")
	payload := []byte{0x0a, 0x0c}
	payload = append(payload, []byte("Decoded body")...)
	payload = append(payload, 0x12, byte(len(linkAndTag)))
	payload = append(payload, linkAndTag...)

	got, err := DecodeZData(payload)
	if err != nil {
		t.Fatalf("DecodeZData: %v", err)
	}
	if got != "Decoded body\n\nhttps://example.com/page #Research" {
		t.Fatalf("decoded = %q", got)
	}
}

func TestDecodeZDataZlibNestedProtoStrings(t *testing.T) {
	t.Parallel()

	nested := []byte{0x0a, 0x0b}
	nested = append(nested, []byte("Nested text")...)
	payload := []byte{0x1a, byte(len(nested))}
	payload = append(payload, nested...)

	var compressed bytes.Buffer
	writer := zlib.NewWriter(&compressed)
	if _, err := writer.Write(payload); err != nil {
		t.Fatalf("zlib write: %v", err)
	}
	if err := writer.Close(); err != nil {
		t.Fatalf("zlib close: %v", err)
	}

	got, err := DecodeZData(compressed.Bytes())
	if err != nil {
		t.Fatalf("DecodeZData: %v", err)
	}
	if got != "Nested text" {
		t.Fatalf("decoded = %q", got)
	}
}
