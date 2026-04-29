package vault

import (
	"strings"
	"testing"
	"time"

	"github.com/darron/dbrain/internal/model"
)

func TestRenderItemIncludesMediaSection(t *testing.T) {
	t.Parallel()

	body, err := RenderItem(model.Item{
		SourceKey:    "x:test",
		SourceType:   "x_bookmark",
		ExternalID:   "123",
		CanonicalURL: "https://x.com/example/status/123",
		Title:        "Media note",
		LinksJSON:    "[]",
		XPostStatus:  "ok_graphql",
		Media: []model.ItemMediaRef{
			{
				Ordinal:        0,
				ExpandedURL:    "https://x.com/example/status/123/photo/1",
				RemoteURL:      "https://pbs.twimg.com/media/test.jpg",
				MediaType:      "photo",
				DownloadStatus: "downloaded",
				LocalPath:      "media/x/photo/ab/test.jpg",
				Width:          1200,
				Height:         800,
			},
		},
	})
	if err != nil {
		t.Fatalf("RenderItem: %v", err)
	}

	for _, want := range []string{
		"## Media",
		"### Photo 1",
		"![[media/x/photo/ab/test.jpg]]",
		"- Status: `downloaded`",
		"- Post Media URL: https://x.com/example/status/123/photo/1",
	} {
		if !strings.Contains(body, want) {
			t.Fatalf("expected rendered note to contain %q\n%s", want, body)
		}
	}
}

func TestRenderItemUsesArchiveURLWhenLocalPruned(t *testing.T) {
	t.Parallel()

	prunedAt, err := time.Parse(time.RFC3339, "2026-04-25T20:00:00Z")
	if err != nil {
		t.Fatalf("parse pruned time: %v", err)
	}

	body, err := RenderItem(model.Item{
		SourceKey:    "x:test-pruned",
		SourceType:   "x_bookmark",
		ExternalID:   "123",
		CanonicalURL: "https://x.com/example/status/123",
		Title:        "Archived media note",
		LinksJSON:    "[]",
		XPostStatus:  "ok_graphql",
		Media: []model.ItemMediaRef{
			{
				Ordinal:         0,
				ExpandedURL:     "https://x.com/example/status/123/photo/1",
				RemoteURL:       "https://pbs.twimg.com/media/test.jpg",
				MediaType:       "photo",
				DownloadStatus:  "downloaded",
				LocalPath:       "media/x/photo/ab/test.jpg",
				ArchiveProvider: "cloudflare_r2",
				ArchiveBucket:   "dbrain",
				ArchiveKey:      "media/x/photo/ab/test.jpg",
				ArchiveStatus:   "archived",
				ArchiveURL:      "https://cdn.example.com/media/x/photo/ab/test.jpg",
				LocalPrunedAt:   prunedAt,
			},
		},
	})
	if err != nil {
		t.Fatalf("RenderItem: %v", err)
	}

	for _, want := range []string{
		"![](https://cdn.example.com/media/x/photo/ab/test.jpg)",
		"[Open archived media](https://cdn.example.com/media/x/photo/ab/test.jpg)",
		"- Archive Status: `archived`",
		"- Archive Provider: `cloudflare_r2`",
		"- Archive Bucket: `dbrain`",
		"- Archive Key: `media/x/photo/ab/test.jpg`",
		"- Archive URL: https://cdn.example.com/media/x/photo/ab/test.jpg",
		"- Local Pruned: 2026-04-25T20:00:00Z",
	} {
		if !strings.Contains(body, want) {
			t.Fatalf("expected rendered note to contain %q\n%s", want, body)
		}
	}
	if strings.Contains(body, "![[media/x/photo/ab/test.jpg]]") {
		t.Fatalf("expected local embed to be omitted for pruned media\n%s", body)
	}
}

func TestRenderItemShowsArchiveMetadataWithoutPublicURL(t *testing.T) {
	t.Parallel()

	prunedAt, err := time.Parse(time.RFC3339, "2026-04-25T20:00:00Z")
	if err != nil {
		t.Fatalf("parse pruned time: %v", err)
	}

	body, err := RenderItem(model.Item{
		SourceKey:    "x:test-auth-archive",
		SourceType:   "x_bookmark",
		ExternalID:   "123",
		CanonicalURL: "https://x.com/example/status/123",
		Title:        "Authenticated archive note",
		LinksJSON:    "[]",
		XPostStatus:  "ok_graphql",
		Media: []model.ItemMediaRef{
			{
				Ordinal:         0,
				ExpandedURL:     "https://x.com/example/status/123/video/1",
				RemoteURL:       "https://video.twimg.com/ext/test.mp4",
				MediaType:       "video",
				DownloadStatus:  "downloaded",
				LocalPath:       "media/x/video/ab/test.mp4",
				ArchiveProvider: "cloudflare_r2",
				ArchiveBucket:   "dbrain",
				ArchiveKey:      "media/x/video/ab/test.mp4",
				ArchiveStatus:   "archived",
				LocalPrunedAt:   prunedAt,
			},
		},
	})
	if err != nil {
		t.Fatalf("RenderItem: %v", err)
	}

	for _, want := range []string{
		"Archived remotely. No anonymous media URL is configured.",
		"- Archive Provider: `cloudflare_r2`",
		"- Archive Bucket: `dbrain`",
		"- Archive Key: `media/x/video/ab/test.mp4`",
		"- Local Pruned: 2026-04-25T20:00:00Z",
	} {
		if !strings.Contains(body, want) {
			t.Fatalf("expected rendered note to contain %q\n%s", want, body)
		}
	}
	if strings.Contains(body, "[Archived media](") || strings.Contains(body, "![[media/x/video/ab/test.mp4]]") {
		t.Fatalf("expected no public archive link or local embed\n%s", body)
	}
}

func TestRenderItemUsesProxyURLForArchivedVideoWithoutPublicURL(t *testing.T) {
	t.Parallel()

	prunedAt, err := time.Parse(time.RFC3339, "2026-04-25T20:00:00Z")
	if err != nil {
		t.Fatalf("parse pruned time: %v", err)
	}

	body, err := RenderItemWithOptions(model.Item{
		SourceKey:    "x:test-auth-archive-proxy",
		SourceType:   "x_bookmark",
		ExternalID:   "123",
		CanonicalURL: "https://x.com/example/status/123",
		Title:        "Authenticated archive note with proxy",
		LinksJSON:    "[]",
		XPostStatus:  "ok_graphql",
		Media: []model.ItemMediaRef{
			{
				MediaAssetID:    2225,
				Ordinal:         0,
				ExpandedURL:     "https://x.com/example/status/123/video/1",
				RemoteURL:       "https://video.twimg.com/ext/test.mp4",
				MediaType:       "video",
				DownloadStatus:  "downloaded",
				LocalPath:       "media/x/video/ab/test.mp4",
				ArchiveProvider: "cloudflare_r2",
				ArchiveBucket:   "dbrain",
				ArchiveKey:      "media/x/video/ab/test.mp4",
				ArchiveStatus:   "archived",
				LocalPrunedAt:   prunedAt,
			},
		},
	}, RenderOptions{
		MediaProxyBaseURL: "http://127.0.0.1:8742",
	})
	if err != nil {
		t.Fatalf("RenderItemWithOptions: %v", err)
	}

	for _, want := range []string{
		`<video controls preload="metadata" src="http://127.0.0.1:8742/media/asset/2225"></video>`,
		`[Open archived media](http://127.0.0.1:8742/media/asset/2225)`,
		"- Archive Provider: `cloudflare_r2`",
		"- Archive Bucket: `dbrain`",
		"- Archive Key: `media/x/video/ab/test.mp4`",
	} {
		if !strings.Contains(body, want) {
			t.Fatalf("expected rendered note to contain %q\n%s", want, body)
		}
	}
	if strings.Contains(body, "No anonymous media URL is configured.") {
		t.Fatalf("expected proxy-backed archive render, got fallback text\n%s", body)
	}
}

func TestRenderItemUsesProxyURLForArchivedPhotoWithoutPublicURL(t *testing.T) {
	t.Parallel()

	prunedAt, err := time.Parse(time.RFC3339, "2026-04-25T20:00:00Z")
	if err != nil {
		t.Fatalf("parse pruned time: %v", err)
	}

	body, err := RenderItemWithOptions(model.Item{
		SourceKey:    "x:test-auth-archive-photo-proxy",
		SourceType:   "x_bookmark",
		ExternalID:   "123",
		CanonicalURL: "https://x.com/example/status/123",
		Title:        "Authenticated archive photo note with proxy",
		LinksJSON:    "[]",
		XPostStatus:  "ok_graphql",
		Media: []model.ItemMediaRef{
			{
				MediaAssetID:    3333,
				Ordinal:         0,
				ExpandedURL:     "https://x.com/example/status/123/photo/1",
				RemoteURL:       "https://pbs.twimg.com/media/test.jpg",
				MediaType:       "photo",
				DownloadStatus:  "downloaded",
				LocalPath:       "media/x/photo/ab/test.jpg",
				ArchiveProvider: "cloudflare_r2",
				ArchiveBucket:   "dbrain",
				ArchiveKey:      "media/x/photo/ab/test.jpg",
				ArchiveStatus:   "archived",
				LocalPrunedAt:   prunedAt,
			},
		},
	}, RenderOptions{
		MediaProxyBaseURL: "http://127.0.0.1:8742",
	})
	if err != nil {
		t.Fatalf("RenderItemWithOptions: %v", err)
	}

	for _, want := range []string{
		"![](http://127.0.0.1:8742/media/asset/3333)",
		"[Open archived media](http://127.0.0.1:8742/media/asset/3333)",
		"- Archive Provider: `cloudflare_r2`",
		"- Archive Bucket: `dbrain`",
		"- Archive Key: `media/x/photo/ab/test.jpg`",
	} {
		if !strings.Contains(body, want) {
			t.Fatalf("expected rendered note to contain %q\n%s", want, body)
		}
	}
	if strings.Contains(body, "No anonymous media URL is configured.") {
		t.Fatalf("expected proxy-backed archive render, got fallback text\n%s", body)
	}
}

func TestRenderItemIncludesQuotedPostSection(t *testing.T) {
	t.Parallel()

	body, err := RenderItem(model.Item{
		SourceKey:    "x:2040000000000000000",
		SourceType:   "x_bookmark",
		ExternalID:   "2040000000000000000",
		CanonicalURL: "https://x.com/parent/status/2040000000000000000",
		Title:        "Quote tweet note",
		LinksJSON:    "[]",
		XPostStatus:  "ok_graphql",
		XPostText:    "Oh this is delicious...",
		XPostJSON: `{
			"snapshot":{
				"id":"2040000000000000000",
				"text":"Oh this is delicious...",
				"quoted_post":{
					"id":"2030838203549184127",
					"text":"Quoted post context that should be rendered explicitly.",
					"author_handle":"quoted",
					"author_name":"Quoted Person",
					"posted_at":"2026-04-25T21:00:00Z",
					"url":"https://x.com/quoted/status/2030838203549184127",
					"links":["https://example.com/quoted"]
				}
			}
		}`,
	})
	if err != nil {
		t.Fatalf("RenderItem: %v", err)
	}

	for _, want := range []string{
		"## Quoted X Post",
		"[[items/x/2026/2030838203549184127.md]]",
		"https://x.com/quoted/status/2030838203549184127",
		"Quoted Person (@quoted)",
		"Quoted post context that should be rendered explicitly.",
		"https://example.com/quoted",
	} {
		if !strings.Contains(body, want) {
			t.Fatalf("expected rendered note to contain %q\n%s", want, body)
		}
	}
}
