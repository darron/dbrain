package web

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/darron/dbrain/internal/model"
)

func TestParseSignedURLTTL(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		raw  string
		want time.Duration
	}{
		{name: "default", raw: "", want: defaultSignedURLTTL},
		{name: "invalid", raw: "soon", want: defaultSignedURLTTL},
		{name: "minimum", raw: "10s", want: time.Minute},
		{name: "maximum", raw: "2h", want: time.Hour},
		{name: "valid", raw: "15m", want: 15 * time.Minute},
	}
	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			if got := parseSignedURLTTL(tt.raw); got != tt.want {
				t.Fatalf("parseSignedURLTTL(%q) = %s, want %s", tt.raw, got, tt.want)
			}
		})
	}
}

func TestParseMediaAssetID(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		target  string
		want    int64
		wantErr string
	}{
		{name: "path", target: "/media/asset/42", want: 42},
		{name: "path slash", target: "/media/asset/42/", want: 42},
		{name: "query", target: "/api/media/signed-url?id=73", want: 73},
		{name: "missing", target: "/api/media/signed-url", wantErr: "media asset id is required"},
		{name: "invalid", target: "/media/asset/nope", wantErr: `invalid media asset id: "nope"`},
		{name: "negative", target: "/media/asset/-1", wantErr: `invalid media asset id: "-1"`},
	}
	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			req := httptest.NewRequest(http.MethodGet, tt.target, nil)
			got, err := parseMediaAssetID(req)
			if tt.wantErr != "" {
				if err == nil || err.Error() != tt.wantErr {
					t.Fatalf("parseMediaAssetID() error = %v, want %q", err, tt.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatalf("parseMediaAssetID() unexpected error: %v", err)
			}
			if got != tt.want {
				t.Fatalf("parseMediaAssetID() = %d, want %d", got, tt.want)
			}
		})
	}
}

func TestWriteArchiveHeaders(t *testing.T) {
	t.Parallel()

	header := http.Header{}
	writeArchiveHeaders(header, model.MediaAsset{
		ArchiveKey: "media/x/photo/test.jpg",
		LocalPath:  "media/local/bad\"name\r\n.jpg",
	}, archiveObject{
		ContentLength: 12,
		ETag:          "etag-1",
		LastModified:  time.Date(2026, time.May, 4, 18, 0, 0, 0, time.UTC),
	})

	if got := header.Get("Content-Type"); got != "image/jpeg" {
		t.Fatalf("Content-Type = %q, want image/jpeg", got)
	}
	if got := header.Get("Content-Length"); got != "12" {
		t.Fatalf("Content-Length = %q, want 12", got)
	}
	if got := header.Get("ETag"); got != `"etag-1"` {
		t.Fatalf("ETag = %q, want quoted etag", got)
	}
	if got := header.Get("Accept-Ranges"); got != "bytes" {
		t.Fatalf("Accept-Ranges = %q, want bytes", got)
	}
	disposition := header.Get("Content-Disposition")
	if !strings.Contains(disposition, `inline; filename="badname.jpg"`) {
		t.Fatalf("Content-Disposition = %q, want sanitized filename", disposition)
	}
}
