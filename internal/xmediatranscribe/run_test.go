package xmediatranscribe

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"dbrain/internal/config"
	"dbrain/internal/itemhash"
	"dbrain/internal/model"
	"dbrain/internal/store"
)

func TestRunTranscribesDownloadedXVideoAndWritesItemNote(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	cfg, err := config.Load(root)
	if err != nil {
		t.Fatalf("config.Load: %v", err)
	}
	if err := cfg.EnsureDirs(); err != nil {
		t.Fatalf("EnsureDirs: %v", err)
	}

	st, err := store.Open(cfg.DBPath)
	if err != nil {
		t.Fatalf("store.Open: %v", err)
	}
	defer func() { _ = st.Close() }()

	now := time.Date(2026, 4, 23, 23, 0, 0, 0, time.UTC)
	itemResult, err := st.UpsertItem(context.Background(), model.Item{
		SourceKey:    "x:test-video-transcript",
		SourceType:   "x_bookmark",
		ExternalID:   "2046654139615326626",
		CanonicalURL: "https://x.com/example/status/2046654139615326626",
		Title:        "Video post",
		ContentHash:  "seed-hash",
		LinksJSON:    "[]",
		NotePath:     "items/x/2026/2046654139615326626.md",
		RawJSON:      `{}`,
		ImportedAt:   now,
		UpdatedAt:    now,
		LastSeenAt:   now,
	})
	if err != nil {
		t.Fatalf("UpsertItem: %v", err)
	}

	hydration := model.XHydration{
		FullText:  "hello",
		Language:  "en",
		Status:    "ok_graphql",
		FetchedAt: now,
		APIJSON: `{
			"snapshot":{
				"media_objects":[
					{"type":"video","url":"https://video.twimg.com/ext/test.mp4","expanded_url":"https://x.com/example/status/2046654139615326626/video/1","width":1280,"height":720}
				]
			}
		}`,
	}
	if _, err := st.SaveXHydration(context.Background(), itemResult.ItemID, hydration); err != nil {
		t.Fatalf("SaveXHydration: %v", err)
	}

	refs, err := st.ListItemMediaRefs(context.Background(), itemResult.ItemID)
	if err != nil {
		t.Fatalf("ListItemMediaRefs: %v", err)
	}
	if len(refs) != 1 {
		t.Fatalf("expected one media ref, got %#v", refs)
	}

	localRel := "media/x/video/aa/test.mp4"
	localAbs := filepath.Join(cfg.VaultDir, filepath.FromSlash(localRel))
	if err := os.MkdirAll(filepath.Dir(localAbs), 0o755); err != nil {
		t.Fatalf("MkdirAll media dir: %v", err)
	}
	if err := os.WriteFile(localAbs, []byte("fake mp4"), 0o644); err != nil {
		t.Fatalf("WriteFile media: %v", err)
	}
	if _, err := st.SaveMediaDownload(context.Background(), refs[0].MediaAssetID, model.MediaDownloadResult{
		MIMEType:     "video/mp4",
		ByteSize:     42,
		ContentHash:  "sha256:test",
		LocalPath:    localRel,
		Status:       "downloaded",
		DownloadedAt: now.Add(time.Minute),
	}); err != nil {
		t.Fatalf("SaveMediaDownload: %v", err)
	}

	ffprobe := installFakeFFprobe(t, "0\n")
	mw := installFakeMacWhisper(t, "hello from the transcript and this is comfortably longer than forty characters\n")

	stats, err := Run(context.Background(), cfg, st, Options{
		Limit:            10,
		MacWhisperBinary: mw,
		FFprobeBinary:    ffprobe,
	})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}

	if stats.ItemsUpdated != 1 {
		t.Fatalf("expected one item update, got %+v", stats)
	}
	if stats.MediaWithAudio != 1 || stats.MediaTranscribed != 1 {
		t.Fatalf("expected one transcribed video, got %+v", stats)
	}

	item, err := st.GetItem(context.Background(), "x:test-video-transcript")
	if err != nil {
		t.Fatalf("GetItem: %v", err)
	}
	if item.ArticleTitle != transcriptArticleTitle {
		t.Fatalf("unexpected article title: %q", item.ArticleTitle)
	}
	if item.XMediaTranscriptStatus != "ok" || item.XMediaTranscriptError != "" {
		t.Fatalf("unexpected transcript state: status=%q error=%q", item.XMediaTranscriptStatus, item.XMediaTranscriptError)
	}
	if !strings.Contains(item.ArticleText, "hello from the transcript and this is comfortably longer than forty characters") {
		t.Fatalf("expected transcript in article text, got %q", item.ArticleText)
	}

	noteBytes, err := os.ReadFile(filepath.Join(cfg.VaultDir, filepath.FromSlash(item.NotePath)))
	if err != nil {
		t.Fatalf("ReadFile note: %v", err)
	}
	note := string(noteBytes)
	if !strings.Contains(note, "hello from the transcript and this is comfortably longer than forty characters") {
		t.Fatalf("expected transcript in note, got %q", note)
	}
}

func TestRunTranscriptSurvivesLaterBlankItemUpsert(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	cfg, err := config.Load(root)
	if err != nil {
		t.Fatalf("config.Load: %v", err)
	}
	if err := cfg.EnsureDirs(); err != nil {
		t.Fatalf("EnsureDirs: %v", err)
	}

	st, err := store.Open(cfg.DBPath)
	if err != nil {
		t.Fatalf("store.Open: %v", err)
	}
	defer func() { _ = st.Close() }()

	now := time.Date(2026, 4, 23, 23, 0, 0, 0, time.UTC)
	itemResult, err := st.UpsertItem(context.Background(), model.Item{
		SourceKey:    "x:test-video-preserve-transcript",
		SourceType:   "x_bookmark",
		ExternalID:   "2046654139615326628",
		CanonicalURL: "https://x.com/example/status/2046654139615326628",
		Title:        "Video post",
		ContentHash:  "seed-hash",
		LinksJSON:    "[]",
		NotePath:     "items/x/2026/2046654139615326628.md",
		RawJSON:      `{}`,
		ImportedAt:   now,
		UpdatedAt:    now,
		LastSeenAt:   now,
	})
	if err != nil {
		t.Fatalf("UpsertItem: %v", err)
	}

	hydration := model.XHydration{
		FullText:  "hello",
		Language:  "en",
		Status:    "ok_graphql",
		FetchedAt: now,
		APIJSON: `{
			"snapshot":{
				"media_objects":[
					{"type":"video","url":"https://video.twimg.com/ext/test-preserve.mp4","expanded_url":"https://x.com/example/status/2046654139615326628/video/1","width":1280,"height":720}
				]
			}
		}`,
	}
	if _, err := st.SaveXHydration(context.Background(), itemResult.ItemID, hydration); err != nil {
		t.Fatalf("SaveXHydration: %v", err)
	}

	refs, err := st.ListItemMediaRefs(context.Background(), itemResult.ItemID)
	if err != nil {
		t.Fatalf("ListItemMediaRefs: %v", err)
	}
	if len(refs) != 1 {
		t.Fatalf("expected one media ref, got %#v", refs)
	}

	localRel := "media/x/video/zz/test-preserve.mp4"
	localAbs := filepath.Join(cfg.VaultDir, filepath.FromSlash(localRel))
	if err := os.MkdirAll(filepath.Dir(localAbs), 0o755); err != nil {
		t.Fatalf("MkdirAll media dir: %v", err)
	}
	if err := os.WriteFile(localAbs, []byte("fake mp4"), 0o644); err != nil {
		t.Fatalf("WriteFile media: %v", err)
	}
	if _, err := st.SaveMediaDownload(context.Background(), refs[0].MediaAssetID, model.MediaDownloadResult{
		MIMEType:     "video/mp4",
		ByteSize:     42,
		ContentHash:  "sha256:test",
		LocalPath:    localRel,
		Status:       "downloaded",
		DownloadedAt: now.Add(time.Minute),
	}); err != nil {
		t.Fatalf("SaveMediaDownload: %v", err)
	}

	ffprobe := installFakeFFprobe(t, "0\n")
	mw := installFakeMacWhisper(t, "hello from the preserved transcript and this is comfortably longer than forty characters\n")

	if _, err := Run(context.Background(), cfg, st, Options{
		Limit:            10,
		MacWhisperBinary: mw,
		FFprobeBinary:    ffprobe,
	}); err != nil {
		t.Fatalf("Run: %v", err)
	}

	item, err := st.GetItem(context.Background(), "x:test-video-preserve-transcript")
	if err != nil {
		t.Fatalf("GetItem after transcription: %v", err)
	}
	if item.ArticleTitle != transcriptArticleTitle || !strings.Contains(item.ArticleText, "preserved transcript") {
		t.Fatalf("expected transcript materialized before later upsert, got title=%q text=%q", item.ArticleTitle, item.ArticleText)
	}

	refreshed := model.Item{
		SourceKey:    item.SourceKey,
		SourceType:   item.SourceType,
		ExternalID:   item.ExternalID,
		CanonicalURL: item.CanonicalURL,
		Title:        "Video post refreshed",
		LinksJSON:    item.LinksJSON,
		NotePath:     item.NotePath,
		RawJSON:      `{"refreshed":true}`,
		ImportedAt:   item.ImportedAt,
		UpdatedAt:    now.Add(2 * time.Hour),
		LastSeenAt:   now.Add(2 * time.Hour),
	}
	refreshed.ContentHash = itemhash.Compute(refreshed)
	if _, err := st.UpsertItem(context.Background(), refreshed); err != nil {
		t.Fatalf("UpsertItem refresh: %v", err)
	}

	item, err = st.GetItem(context.Background(), "x:test-video-preserve-transcript")
	if err != nil {
		t.Fatalf("GetItem after refresh: %v", err)
	}
	if item.ArticleTitle != transcriptArticleTitle {
		t.Fatalf("expected transcript article title preserved, got %q", item.ArticleTitle)
	}
	if !strings.Contains(item.ArticleText, "preserved transcript") {
		t.Fatalf("expected transcript article text preserved, got %q", item.ArticleText)
	}
	if item.XMediaTranscriptStatus != "ok" {
		t.Fatalf("expected transcript status to remain ok, got %q", item.XMediaTranscriptStatus)
	}
}

func TestRunSkipsDownloadedXVideoWithoutAudio(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	cfg, err := config.Load(root)
	if err != nil {
		t.Fatalf("config.Load: %v", err)
	}
	if err := cfg.EnsureDirs(); err != nil {
		t.Fatalf("EnsureDirs: %v", err)
	}

	st, err := store.Open(cfg.DBPath)
	if err != nil {
		t.Fatalf("store.Open: %v", err)
	}
	defer func() { _ = st.Close() }()

	now := time.Date(2026, 4, 23, 23, 0, 0, 0, time.UTC)
	itemResult, err := st.UpsertItem(context.Background(), model.Item{
		SourceKey:    "x:test-video-no-audio",
		SourceType:   "x_bookmark",
		ExternalID:   "2046654139615326627",
		CanonicalURL: "https://x.com/example/status/2046654139615326627",
		Title:        "Silent video post",
		ContentHash:  "seed-hash",
		LinksJSON:    "[]",
		NotePath:     "items/x/2026/2046654139615326627.md",
		RawJSON:      `{}`,
		ImportedAt:   now,
		UpdatedAt:    now,
		LastSeenAt:   now,
	})
	if err != nil {
		t.Fatalf("UpsertItem: %v", err)
	}

	hydration := model.XHydration{
		FullText:  "hello",
		Language:  "en",
		Status:    "ok_graphql",
		FetchedAt: now,
		APIJSON: `{
			"snapshot":{
				"media_objects":[
					{"type":"video","url":"https://video.twimg.com/ext/test-no-audio.mp4","expanded_url":"https://x.com/example/status/2046654139615326627/video/1","width":1280,"height":720}
				]
			}
		}`,
	}
	if _, err := st.SaveXHydration(context.Background(), itemResult.ItemID, hydration); err != nil {
		t.Fatalf("SaveXHydration: %v", err)
	}

	refs, err := st.ListItemMediaRefs(context.Background(), itemResult.ItemID)
	if err != nil {
		t.Fatalf("ListItemMediaRefs: %v", err)
	}
	localRel := "media/x/video/bb/test-no-audio.mp4"
	localAbs := filepath.Join(cfg.VaultDir, filepath.FromSlash(localRel))
	if err := os.MkdirAll(filepath.Dir(localAbs), 0o755); err != nil {
		t.Fatalf("MkdirAll media dir: %v", err)
	}
	if err := os.WriteFile(localAbs, []byte("fake mp4"), 0o644); err != nil {
		t.Fatalf("WriteFile media: %v", err)
	}
	if _, err := st.SaveMediaDownload(context.Background(), refs[0].MediaAssetID, model.MediaDownloadResult{
		MIMEType:     "video/mp4",
		ByteSize:     42,
		ContentHash:  "sha256:test",
		LocalPath:    localRel,
		Status:       "downloaded",
		DownloadedAt: now.Add(time.Minute),
	}); err != nil {
		t.Fatalf("SaveMediaDownload: %v", err)
	}

	ffprobe := installFakeFFprobe(t, "")
	mw := installFakeMacWhisper(t, "should not be used\n")

	stats, err := Run(context.Background(), cfg, st, Options{
		Limit:            10,
		MacWhisperBinary: mw,
		FFprobeBinary:    ffprobe,
	})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}

	if stats.ItemsSkipped == 0 {
		t.Fatalf("expected skipped item, got %+v", stats)
	}
	if stats.MediaWithAudio != 0 || stats.MediaTranscribed != 0 {
		t.Fatalf("expected no transcribed media, got %+v", stats)
	}

	item, err := st.GetItem(context.Background(), "x:test-video-no-audio")
	if err != nil {
		t.Fatalf("GetItem: %v", err)
	}
	if item.ArticleText != "" {
		t.Fatalf("expected empty article text, got %q", item.ArticleText)
	}
	if item.XMediaTranscriptStatus != "no_audio" || item.XMediaTranscriptError != "" {
		t.Fatalf("unexpected transcript state: status=%q error=%q", item.XMediaTranscriptStatus, item.XMediaTranscriptError)
	}

	stats, err = Run(context.Background(), cfg, st, Options{
		Limit:            10,
		MacWhisperBinary: mw,
		FFprobeBinary:    ffprobe,
	})
	if err != nil {
		t.Fatalf("second Run: %v", err)
	}
	if stats.ItemsQueued != 0 {
		t.Fatalf("expected no retry queue after no-audio state, got %+v", stats)
	}
}

func TestRunSkipsNoiseMarkerTranscript(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	cfg, err := config.Load(root)
	if err != nil {
		t.Fatalf("config.Load: %v", err)
	}
	if err := cfg.EnsureDirs(); err != nil {
		t.Fatalf("EnsureDirs: %v", err)
	}

	st, err := store.Open(cfg.DBPath)
	if err != nil {
		t.Fatalf("store.Open: %v", err)
	}
	defer func() { _ = st.Close() }()

	now := time.Date(2026, 4, 24, 6, 0, 0, 0, time.UTC)
	itemResult, err := st.UpsertItem(context.Background(), model.Item{
		SourceKey:    "x:test-video-music-only",
		SourceType:   "x_bookmark",
		ExternalID:   "2048000000000000001",
		CanonicalURL: "https://x.com/example/status/2048000000000000001",
		Title:        "Music only video",
		ContentHash:  "seed-hash",
		LinksJSON:    "[]",
		NotePath:     "items/x/2026/2048000000000000001.md",
		RawJSON:      `{}`,
		ImportedAt:   now,
		UpdatedAt:    now,
		LastSeenAt:   now,
	})
	if err != nil {
		t.Fatalf("UpsertItem: %v", err)
	}

	hydration := model.XHydration{
		FullText:  "hello",
		Language:  "en",
		Status:    "ok_graphql",
		FetchedAt: now,
		APIJSON: `{
			"snapshot":{
				"media_objects":[
					{"type":"video","url":"https://video.twimg.com/ext/music-only.mp4","expanded_url":"https://x.com/example/status/2048000000000000001/video/1","width":1280,"height":720}
				]
			}
		}`,
	}
	if _, err := st.SaveXHydration(context.Background(), itemResult.ItemID, hydration); err != nil {
		t.Fatalf("SaveXHydration: %v", err)
	}

	refs, err := st.ListItemMediaRefs(context.Background(), itemResult.ItemID)
	if err != nil {
		t.Fatalf("ListItemMediaRefs: %v", err)
	}
	localRel := "media/x/video/cc/music-only.mp4"
	localAbs := filepath.Join(cfg.VaultDir, filepath.FromSlash(localRel))
	if err := os.MkdirAll(filepath.Dir(localAbs), 0o755); err != nil {
		t.Fatalf("MkdirAll media dir: %v", err)
	}
	if err := os.WriteFile(localAbs, []byte("fake mp4"), 0o644); err != nil {
		t.Fatalf("WriteFile media: %v", err)
	}
	if _, err := st.SaveMediaDownload(context.Background(), refs[0].MediaAssetID, model.MediaDownloadResult{
		MIMEType:     "video/mp4",
		ByteSize:     42,
		ContentHash:  "sha256:test",
		LocalPath:    localRel,
		Status:       "downloaded",
		DownloadedAt: now.Add(time.Minute),
	}); err != nil {
		t.Fatalf("SaveMediaDownload: %v", err)
	}

	ffprobe := installFakeFFprobe(t, "0\n")
	mw := installFakeMacWhisper(t, "[Music]\n")

	stats, err := Run(context.Background(), cfg, st, Options{
		Limit:            10,
		MacWhisperBinary: mw,
		FFprobeBinary:    ffprobe,
	})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}

	if stats.ItemsSkipped == 0 {
		t.Fatalf("expected skipped item, got %+v", stats)
	}
	if stats.MediaWithAudio != 1 || stats.MediaTranscribed != 0 {
		t.Fatalf("expected audio detected but transcript rejected, got %+v", stats)
	}

	item, err := st.GetItem(context.Background(), "x:test-video-music-only")
	if err != nil {
		t.Fatalf("GetItem: %v", err)
	}
	if item.ArticleText != "" || item.ArticleTitle != "" {
		t.Fatalf("expected no saved transcript, got title=%q text=%q", item.ArticleTitle, item.ArticleText)
	}
	if item.XMediaTranscriptStatus != "noise" || item.XMediaTranscriptError != "" {
		t.Fatalf("unexpected transcript state: status=%q error=%q", item.XMediaTranscriptStatus, item.XMediaTranscriptError)
	}
}

func TestRunSkipsTooShortTranscript(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	cfg, err := config.Load(root)
	if err != nil {
		t.Fatalf("config.Load: %v", err)
	}
	if err := cfg.EnsureDirs(); err != nil {
		t.Fatalf("EnsureDirs: %v", err)
	}

	st, err := store.Open(cfg.DBPath)
	if err != nil {
		t.Fatalf("store.Open: %v", err)
	}
	defer func() { _ = st.Close() }()

	now := time.Date(2026, 4, 24, 6, 5, 0, 0, time.UTC)
	itemResult, err := st.UpsertItem(context.Background(), model.Item{
		SourceKey:    "x:test-video-too-short",
		SourceType:   "x_bookmark",
		ExternalID:   "2048000000000000002",
		CanonicalURL: "https://x.com/example/status/2048000000000000002",
		Title:        "Short clip",
		ContentHash:  "seed-hash",
		LinksJSON:    "[]",
		NotePath:     "items/x/2026/2048000000000000002.md",
		RawJSON:      `{}`,
		ImportedAt:   now,
		UpdatedAt:    now,
		LastSeenAt:   now,
	})
	if err != nil {
		t.Fatalf("UpsertItem: %v", err)
	}

	hydration := model.XHydration{
		FullText:  "hello",
		Language:  "en",
		Status:    "ok_graphql",
		FetchedAt: now,
		APIJSON: `{
			"snapshot":{
				"media_objects":[
					{"type":"video","url":"https://video.twimg.com/ext/short.mp4","expanded_url":"https://x.com/example/status/2048000000000000002/video/1","width":1280,"height":720}
				]
			}
		}`,
	}
	if _, err := st.SaveXHydration(context.Background(), itemResult.ItemID, hydration); err != nil {
		t.Fatalf("SaveXHydration: %v", err)
	}

	refs, err := st.ListItemMediaRefs(context.Background(), itemResult.ItemID)
	if err != nil {
		t.Fatalf("ListItemMediaRefs: %v", err)
	}
	localRel := "media/x/video/dd/short.mp4"
	localAbs := filepath.Join(cfg.VaultDir, filepath.FromSlash(localRel))
	if err := os.MkdirAll(filepath.Dir(localAbs), 0o755); err != nil {
		t.Fatalf("MkdirAll media dir: %v", err)
	}
	if err := os.WriteFile(localAbs, []byte("fake mp4"), 0o644); err != nil {
		t.Fatalf("WriteFile media: %v", err)
	}
	if _, err := st.SaveMediaDownload(context.Background(), refs[0].MediaAssetID, model.MediaDownloadResult{
		MIMEType:     "video/mp4",
		ByteSize:     42,
		ContentHash:  "sha256:test",
		LocalPath:    localRel,
		Status:       "downloaded",
		DownloadedAt: now.Add(time.Minute),
	}); err != nil {
		t.Fatalf("SaveMediaDownload: %v", err)
	}

	ffprobe := installFakeFFprobe(t, "0\n")
	mw := installFakeMacWhisper(t, "hello there\n")

	stats, err := Run(context.Background(), cfg, st, Options{
		Limit:            10,
		MacWhisperBinary: mw,
		FFprobeBinary:    ffprobe,
	})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}

	if stats.ItemsSkipped == 0 {
		t.Fatalf("expected skipped item, got %+v", stats)
	}
	if stats.MediaWithAudio != 1 || stats.MediaTranscribed != 0 {
		t.Fatalf("expected transcript rejection, got %+v", stats)
	}

	item, err := st.GetItem(context.Background(), "x:test-video-too-short")
	if err != nil {
		t.Fatalf("GetItem: %v", err)
	}
	if item.ArticleText != "" || item.ArticleTitle != "" {
		t.Fatalf("expected no saved transcript, got title=%q text=%q", item.ArticleTitle, item.ArticleText)
	}
	if item.XMediaTranscriptStatus != "too_short" || item.XMediaTranscriptError != "" {
		t.Fatalf("unexpected transcript state: status=%q error=%q", item.XMediaTranscriptStatus, item.XMediaTranscriptError)
	}
}

func TestRunPersistsEmptyTranscriptStateAndDoesNotRetry(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	cfg, err := config.Load(root)
	if err != nil {
		t.Fatalf("config.Load: %v", err)
	}
	if err := cfg.EnsureDirs(); err != nil {
		t.Fatalf("EnsureDirs: %v", err)
	}

	st, err := store.Open(cfg.DBPath)
	if err != nil {
		t.Fatalf("store.Open: %v", err)
	}
	defer func() { _ = st.Close() }()

	now := time.Date(2026, 4, 24, 6, 10, 0, 0, time.UTC)
	itemResult, err := st.UpsertItem(context.Background(), model.Item{
		SourceKey:    "x:test-video-empty-transcript",
		SourceType:   "x_bookmark",
		ExternalID:   "2048000000000000003",
		CanonicalURL: "https://x.com/example/status/2048000000000000003",
		Title:        "Empty transcript clip",
		ContentHash:  "seed-hash",
		LinksJSON:    "[]",
		NotePath:     "items/x/2026/2048000000000000003.md",
		RawJSON:      `{}`,
		ImportedAt:   now,
		UpdatedAt:    now,
		LastSeenAt:   now,
	})
	if err != nil {
		t.Fatalf("UpsertItem: %v", err)
	}

	hydration := model.XHydration{
		FullText:  "hello",
		Language:  "en",
		Status:    "ok_graphql",
		FetchedAt: now,
		APIJSON: `{
			"snapshot":{
				"media_objects":[
					{"type":"video","url":"https://video.twimg.com/ext/empty.mp4","expanded_url":"https://x.com/example/status/2048000000000000003/video/1","width":1280,"height":720}
				]
			}
		}`,
	}
	if _, err := st.SaveXHydration(context.Background(), itemResult.ItemID, hydration); err != nil {
		t.Fatalf("SaveXHydration: %v", err)
	}

	refs, err := st.ListItemMediaRefs(context.Background(), itemResult.ItemID)
	if err != nil {
		t.Fatalf("ListItemMediaRefs: %v", err)
	}
	localRel := "media/x/video/ee/empty.mp4"
	localAbs := filepath.Join(cfg.VaultDir, filepath.FromSlash(localRel))
	if err := os.MkdirAll(filepath.Dir(localAbs), 0o755); err != nil {
		t.Fatalf("MkdirAll media dir: %v", err)
	}
	if err := os.WriteFile(localAbs, []byte("fake mp4"), 0o644); err != nil {
		t.Fatalf("WriteFile media: %v", err)
	}
	if _, err := st.SaveMediaDownload(context.Background(), refs[0].MediaAssetID, model.MediaDownloadResult{
		MIMEType:     "video/mp4",
		ByteSize:     42,
		ContentHash:  "sha256:test",
		LocalPath:    localRel,
		Status:       "downloaded",
		DownloadedAt: now.Add(time.Minute),
	}); err != nil {
		t.Fatalf("SaveMediaDownload: %v", err)
	}

	ffprobe := installFakeFFprobe(t, "0\n")
	mw := installFakeMacWhisper(t, "")

	stats, err := Run(context.Background(), cfg, st, Options{
		Limit:            10,
		MacWhisperBinary: mw,
		FFprobeBinary:    ffprobe,
	})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if stats.ItemsSkipped == 0 {
		t.Fatalf("expected skipped item, got %+v", stats)
	}

	item, err := st.GetItem(context.Background(), "x:test-video-empty-transcript")
	if err != nil {
		t.Fatalf("GetItem: %v", err)
	}
	if item.XMediaTranscriptStatus != "empty" {
		t.Fatalf("unexpected transcript state: status=%q error=%q", item.XMediaTranscriptStatus, item.XMediaTranscriptError)
	}

	stats, err = Run(context.Background(), cfg, st, Options{
		Limit:            10,
		MacWhisperBinary: mw,
		FFprobeBinary:    ffprobe,
	})
	if err != nil {
		t.Fatalf("second Run: %v", err)
	}
	if stats.ItemsQueued != 0 {
		t.Fatalf("expected no retry queue after empty transcript state, got %+v", stats)
	}
}

func installFakeFFprobe(t *testing.T, stdout string) string {
	t.Helper()

	path := filepath.Join(t.TempDir(), "ffprobe")
	script := "#!/bin/sh\nprintf '%s' " + shellQuote(stdout) + "\n"
	if err := os.WriteFile(path, []byte(script), 0o755); err != nil {
		t.Fatalf("write fake ffprobe: %v", err)
	}
	return path
}

func installFakeMacWhisper(t *testing.T, stdout string) string {
	t.Helper()

	path := filepath.Join(t.TempDir(), "mw")
	script := "#!/bin/sh\nprintf '%s' " + shellQuote(stdout) + "\n"
	if err := os.WriteFile(path, []byte(script), 0o755); err != nil {
		t.Fatalf("write fake mw: %v", err)
	}
	return path
}

func shellQuote(value string) string {
	return "'" + strings.ReplaceAll(value, "'", "'\"'\"'") + "'"
}
