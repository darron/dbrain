package linkextract

import "testing"

func TestNormalizeCandidateCanonicalizesCommonSourceURLs(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name          string
		raw           string
		wantCanonical string
		wantType      string
		wantDomain    string
		wantOK        bool
	}{
		{
			name:          "web strips tracking and forces https",
			raw:           "http://www.Example.com/path/../post?utm_source=x&keep=1#frag",
			wantCanonical: "https://example.com/post?keep=1",
			wantType:      "web",
			wantDomain:    "example.com",
			wantOK:        true,
		},
		{
			name:          "github trims to repo",
			raw:           "https://github.com/darron/dbrain/issues/1?tab=readme",
			wantCanonical: "https://github.com/darron/dbrain",
			wantType:      "github",
			wantDomain:    "github.com",
			wantOK:        true,
		},
		{
			name:          "youtube drops timestamp noise",
			raw:           "https://youtu.be/abc123?t=30&si=tracking&list=playlist",
			wantCanonical: "https://youtu.be/abc123?list=playlist",
			wantType:      "youtube",
			wantDomain:    "youtu.be",
			wantOK:        true,
		},
		{
			name:          "x article is retained",
			raw:           "https://twitter.com/i/article/123",
			wantCanonical: "https://x.com/i/article/123",
			wantType:      "x_article",
			wantDomain:    "x.com",
			wantOK:        true,
		},
		{
			name:   "x post is skipped",
			raw:    "https://x.com/example/status/123",
			wantOK: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			got, ok := NormalizeCandidate(tt.raw)
			if ok != tt.wantOK {
				t.Fatalf("ok = %t, want %t", ok, tt.wantOK)
			}
			if !tt.wantOK {
				return
			}
			if got.CanonicalURL != tt.wantCanonical {
				t.Fatalf("CanonicalURL = %q, want %q", got.CanonicalURL, tt.wantCanonical)
			}
			if got.SourceType != tt.wantType {
				t.Fatalf("SourceType = %q, want %q", got.SourceType, tt.wantType)
			}
			if got.Domain != tt.wantDomain {
				t.Fatalf("Domain = %q, want %q", got.Domain, tt.wantDomain)
			}
			if got.SourceKey == "" || got.NotePath == "" {
				t.Fatalf("expected source key and note path, got %#v", got)
			}
		})
	}
}
