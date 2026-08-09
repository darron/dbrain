package mediaarchive

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/darron/dbrain/internal/config"
	"github.com/darron/dbrain/internal/model"
	"github.com/darron/dbrain/internal/store"
)

type fakeUploader struct {
	uploaded int
}

func (f *fakeUploader) Upload(_ context.Context, _ config.Config, asset model.MediaAsset, opts Options) (model.MediaArchiveResult, bool, error) {
	f.uploaded++
	result := archiveResultForAsset(asset, opts)
	result.ETag = "etag:" + asset.LocalPath
	return result, true, nil
}

func TestRunMarksArchivedAndPrunesLocalFile(t *testing.T) {
	t.Parallel()

	cfg, st, itemID, localPath := setupArchivedPhotoItem(t, "x:archive-photo", "https://pbs.twimg.com/media/archive-photo.jpg", true)

	stats, err := Run(context.Background(), cfg, st, Options{
		Bucket:        "dbrain",
		PublicBaseURL: "https://cdn.example.com",
		PruneLocal:    true,
	})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if stats.Candidates != 1 || stats.Archived != 1 || stats.LocalFilesPruned != 1 {
		t.Fatalf("unexpected archive stats: %+v", stats)
	}

	refs, err := st.ListItemMediaRefs(context.Background(), itemID)
	if err != nil {
		t.Fatalf("ListItemMediaRefs: %v", err)
	}
	if len(refs) != 1 {
		t.Fatalf("expected one media ref, got %#v", refs)
	}
	if refs[0].ArchiveStatus != "archived" || refs[0].ArchiveURL != "https://cdn.example.com/"+localPath {
		t.Fatalf("unexpected archived ref: %+v", refs[0])
	}
	if refs[0].LocalPrunedAt.IsZero() {
		t.Fatalf("expected local pruned timestamp, got %+v", refs[0])
	}

	assets, err := st.ListMediaAssetsByLocalPath(context.Background(), localPath)
	if err != nil {
		t.Fatalf("ListMediaAssetsByLocalPath: %v", err)
	}
	if len(assets) != 1 {
		t.Fatalf("expected one asset by local path, got %#v", assets)
	}
	if assets[0].ArchiveBucket != "dbrain" || assets[0].ArchiveKey != localPath {
		t.Fatalf("unexpected archived asset: %+v", assets[0])
	}
	if _, err := os.Stat(filepath.Join(cfg.VaultDir, filepath.FromSlash(localPath))); !os.IsNotExist(err) {
		t.Fatalf("expected pruned file to be removed, stat err=%v", err)
	}
	noteBody, err := os.ReadFile(filepath.Join(cfg.VaultDir, "items/x/2026/archive-photo.md"))
	if err != nil {
		t.Fatalf("read refreshed note: %v", err)
	}
	body := string(noteBody)
	if !strings.Contains(body, "![](https://cdn.example.com/"+localPath+")") {
		t.Fatalf("expected refreshed note to switch to remote media embed\n%s", body)
	}
	if strings.Contains(body, "![["+localPath+"]]") {
		t.Fatalf("expected refreshed note to remove local media embed\n%s", body)
	}
}

func TestRunDefersPruneWhenSameLocalPathStillNeedsCoverage(t *testing.T) {
	t.Parallel()

	cfg, st, _, localPath := setupArchivedPhotoItem(t, "x:archive-shared-a", "https://pbs.twimg.com/media/shared-a.jpg", true)
	_, _, _, _ = setupArchivedPhotoItemWithSharedPath(t, cfg, st, "x:archive-shared-b", "https://pbs.twimg.com/media/shared-b.jpg", localPath, false)

	stats, err := Run(context.Background(), cfg, st, Options{
		Bucket:        "dbrain",
		PublicBaseURL: "https://cdn.example.com",
		PruneLocal:    true,
	})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if stats.LocalFilesPruned != 0 || stats.PruneSkipped == 0 {
		t.Fatalf("expected prune deferral for shared local path, got %+v", stats)
	}

	assets, err := st.ListMediaAssetsByLocalPath(context.Background(), localPath)
	if err != nil {
		t.Fatalf("ListMediaAssetsByLocalPath: %v", err)
	}
	if len(assets) != 2 {
		t.Fatalf("expected two assets sharing path, got %#v", assets)
	}
	for _, asset := range assets {
		if !asset.LocalPrunedAt.IsZero() {
			t.Fatalf("expected shared path to remain local, got %+v", asset)
		}
	}
}

func TestRunArchivesBlueskyAndMastodonSharedPathBeforePruneAndRefreshesBothProjections(t *testing.T) {
	cfg, err := config.Load(t.TempDir())
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if err := cfg.EnsureDirs(); err != nil {
		t.Fatalf("EnsureDirs: %v", err)
	}
	st, err := store.Open(cfg.DBPath)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() { _ = st.Close() })

	localPath := "media/shared/photo/content-hash.jpg"
	bskyItemID, bskyAssetID, bskyNote := setupSocialArchivedPhotoWithSharedPath(t, cfg, st,
		"bsky:archive-shared", "bsky_bookmark", "https://cdn.bsky.example/shared.jpg", localPath)
	mastodonItemID, mastodonAssetID, mastodonNote := setupSocialArchivedPhotoWithSharedPath(t, cfg, st,
		"mastodon:archive-shared", "mastodon_bookmark", "https://mastodon.example/media/shared.jpg", localPath)

	stats, err := Run(t.Context(), cfg, st, Options{
		Bucket:        "dbrain",
		PublicBaseURL: "https://archive.example",
		PruneLocal:    true,
	})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if stats.Candidates != 2 || stats.Archived != 2 || stats.LocalFilesPruned != 1 || stats.LocalRowsPruned != 2 || stats.PruneSkipped != 1 {
		t.Fatalf("shared social archive stats = %+v", stats)
	}
	if _, err := os.Stat(filepath.Join(cfg.VaultDir, filepath.FromSlash(localPath))); !os.IsNotExist(err) {
		t.Fatalf("shared local file still exists or stat failed: %v", err)
	}
	for _, check := range []struct {
		itemID  int64
		assetID int64
		note    string
	}{
		{itemID: bskyItemID, assetID: bskyAssetID, note: bskyNote},
		{itemID: mastodonItemID, assetID: mastodonAssetID, note: mastodonNote},
	} {
		asset, err := st.GetMediaAsset(t.Context(), check.assetID)
		if err != nil {
			t.Fatalf("GetMediaAsset(%d): %v", check.assetID, err)
		}
		if asset.ArchiveStatus != model.MediaArchiveStatusArchived || asset.LocalPrunedAt.IsZero() {
			t.Fatalf("asset was not archived before shared prune: %+v", asset)
		}
		refs, err := st.ListItemMediaRefs(t.Context(), check.itemID)
		if err != nil || len(refs) != 1 || refs[0].ArchiveURL != "https://archive.example/"+localPath {
			t.Fatalf("archived item refs=%+v err=%v", refs, err)
		}
		body, err := os.ReadFile(filepath.Join(cfg.VaultDir, filepath.FromSlash(check.note)))
		if err != nil {
			t.Fatalf("read refreshed note %s: %v", check.note, err)
		}
		if !strings.Contains(string(body), "![](https://archive.example/"+localPath+")") || strings.Contains(string(body), "![["+localPath+"]]") {
			t.Fatalf("projection %s was not refreshed after prune:\n%s", check.note, body)
		}
	}
}

func setupSocialArchivedPhotoWithSharedPath(t *testing.T, cfg config.Config, st *store.Store, sourceKey, sourceType, remoteURL, localPath string) (int64, int64, string) {
	t.Helper()
	now := time.Date(2026, 8, 9, 16, 0, 0, 0, time.UTC)
	namespace := "bsky"
	if strings.HasPrefix(sourceType, "mastodon_") {
		namespace = "mastodon"
	}
	notePath := "items/" + namespace + "/2026/archive-shared.md"
	item, err := st.UpsertItem(t.Context(), model.Item{
		SourceKey: sourceKey, SourceType: sourceType, ExternalID: sourceKey,
		CanonicalURL: "https://social.example/" + sourceKey, Title: sourceKey, Text: "shared photo",
		ContentHash: sourceKey + "-hash", LinksJSON: "[]", NotePath: notePath, RawJSON: "{}",
		ImportedAt: now, UpdatedAt: now, LastSeenAt: now,
	})
	if err != nil {
		t.Fatalf("UpsertItem %s: %v", sourceKey, err)
	}
	if _, err := st.SaveItemMediaCandidates(t.Context(), item.ItemID, []model.MediaCandidate{{
		RemoteURL: remoteURL, MediaType: "photo", ExpandedURL: "https://social.example/" + sourceKey + "/media/1", Width: 1200, Height: 800,
	}}); err != nil {
		t.Fatalf("SaveItemMediaCandidates %s: %v", sourceKey, err)
	}
	refs, err := st.ListItemMediaRefs(t.Context(), item.ItemID)
	if err != nil || len(refs) != 1 {
		t.Fatalf("ListItemMediaRefs %s: refs=%+v err=%v", sourceKey, refs, err)
	}
	fullPath := filepath.Join(cfg.VaultDir, filepath.FromSlash(localPath))
	if err := os.MkdirAll(filepath.Dir(fullPath), 0o755); err != nil {
		t.Fatalf("MkdirAll shared media: %v", err)
	}
	if err := os.WriteFile(fullPath, []byte("identical-photo-bytes"), 0o644); err != nil {
		t.Fatalf("WriteFile shared media: %v", err)
	}
	if _, err := st.SaveMediaDownload(t.Context(), refs[0].MediaAssetID, model.MediaDownloadResult{
		MIMEType: "image/jpeg", ByteSize: int64(len("identical-photo-bytes")), ContentHash: "sha256:shared",
		LocalPath: localPath, Status: model.MediaDownloadStatusDownloaded, DownloadedAt: now,
	}); err != nil {
		t.Fatalf("SaveMediaDownload %s: %v", sourceKey, err)
	}
	if _, err := st.SaveItemOCR(t.Context(), item.ItemID, model.OCRResult{
		Text: "shared photo text", Status: model.ItemOCRStatusOK, Model: "test/ocr", Tool: "test", ToolVersion: "v1", FetchedAt: now,
	}, "sha256:shared-ocr"); err != nil {
		t.Fatalf("SaveItemOCR %s: %v", sourceKey, err)
	}
	return item.ItemID, refs[0].MediaAssetID, notePath
}

func TestPruneLocalPathRejectsTraversalAndSymlinkEscape(t *testing.T) {
	for _, tc := range []struct {
		name      string
		localPath func(t *testing.T, cfg config.Config) (string, string)
	}{
		{
			name: "traversal",
			localPath: func(t *testing.T, cfg config.Config) (string, string) {
				t.Helper()
				outside := filepath.Join(filepath.Dir(cfg.VaultDir), "outside-traversal.jpg")
				rel, err := filepath.Rel(cfg.VaultDir, outside)
				if err != nil {
					t.Fatalf("relative traversal path: %v", err)
				}
				return filepath.ToSlash(rel), outside
			},
		},
		{
			name: "escaping parent symlink",
			localPath: func(t *testing.T, cfg config.Config) (string, string) {
				t.Helper()
				outsideDir := filepath.Join(filepath.Dir(cfg.VaultDir), "outside-symlink")
				if err := os.MkdirAll(outsideDir, 0o755); err != nil {
					t.Fatalf("mkdir outside: %v", err)
				}
				if err := os.Symlink(outsideDir, filepath.Join(cfg.VaultDir, "escape")); err != nil {
					t.Fatalf("symlink outside dir: %v", err)
				}
				return "escape/outside-symlink.jpg", filepath.Join(outsideDir, "outside-symlink.jpg")
			},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			cfg, err := config.Load(t.TempDir())
			if err != nil {
				t.Fatalf("Load: %v", err)
			}
			if err := cfg.EnsureDirs(); err != nil {
				t.Fatalf("EnsureDirs: %v", err)
			}
			st, err := store.Open(cfg.DBPath)
			if err != nil {
				t.Fatalf("Open: %v", err)
			}
			defer func() { _ = st.Close() }()

			localPath, outside := tc.localPath(t, cfg)
			_, assetID, _, _ := setupArchivedPhotoItemWithSharedPath(t, cfg, st, "x:prune-escape", "https://pbs.twimg.com/media/prune-escape.jpg", localPath, true)
			if _, err := st.SaveMediaArchive(t.Context(), assetID, model.MediaArchiveResult{
				Provider:   "s3",
				Bucket:     "dbrain",
				Key:        localPath,
				Status:     model.MediaArchiveStatusArchived,
				ArchivedAt: time.Now().UTC(),
			}); err != nil {
				t.Fatalf("SaveMediaArchive: %v", err)
			}

			pruned, _, err := pruneLocalPathIfSafe(t.Context(), cfg, st, localPath, nil)
			if err == nil {
				t.Error("expected escaping local path to be rejected")
			}
			if pruned {
				t.Error("escaping local path reported pruned")
			}
			data, readErr := os.ReadFile(outside)
			if readErr != nil {
				t.Fatalf("outside sentinel was deleted: %v", readErr)
			}
			if string(data) != "photo-bytes" {
				t.Fatalf("outside sentinel changed to %q", data)
			}
			assets, err := st.ListMediaAssetsByLocalPath(t.Context(), localPath)
			if err != nil {
				t.Fatalf("ListMediaAssetsByLocalPath: %v", err)
			}
			if len(assets) != 1 || !assets[0].LocalPrunedAt.IsZero() {
				t.Fatalf("escaping local path was marked pruned: %+v", assets)
			}
		})
	}
}

func TestRunPrunesWithoutPublicBaseURL(t *testing.T) {
	t.Parallel()

	cfg, st, itemID, localPath := setupArchivedPhotoItem(t, "x:archive-private-photo", "https://pbs.twimg.com/media/archive-private-photo.jpg", true)

	stats, err := Run(context.Background(), cfg, st, Options{
		Bucket:     "dbrain",
		PruneLocal: true,
	})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if stats.Candidates != 1 || stats.Archived != 1 || stats.LocalFilesPruned != 1 {
		t.Fatalf("unexpected archive stats: %+v", stats)
	}

	refs, err := st.ListItemMediaRefs(context.Background(), itemID)
	if err != nil {
		t.Fatalf("ListItemMediaRefs: %v", err)
	}
	if len(refs) != 1 {
		t.Fatalf("expected one media ref, got %#v", refs)
	}
	if refs[0].ArchiveStatus != "archived" || refs[0].ArchiveBucket != "dbrain" || refs[0].ArchiveKey != localPath {
		t.Fatalf("unexpected archived ref: %+v", refs[0])
	}
	if refs[0].ArchiveURL != "" {
		t.Fatalf("expected no public archive url, got %+v", refs[0])
	}
	if refs[0].LocalPrunedAt.IsZero() {
		t.Fatalf("expected local pruned timestamp, got %+v", refs[0])
	}
}

func TestRunUploadsBeforePrune(t *testing.T) {
	t.Parallel()

	cfg, st, itemID, localPath := setupArchivedPhotoItem(t, "x:archive-upload-photo", "https://pbs.twimg.com/media/archive-upload-photo.jpg", true)
	uploader := &fakeUploader{}

	stats, err := Run(context.Background(), cfg, st, Options{
		Bucket:      "dbrain",
		Upload:      true,
		PruneLocal:  true,
		Uploader:    uploader,
		Endpoint:    "https://example.invalid",
		Region:      "auto",
		AccessKeyID: "key",
		SecretKey:   "secret",
	})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if stats.Uploaded != 1 || uploader.uploaded != 1 {
		t.Fatalf("expected one uploaded asset, got stats=%+v uploader=%d", stats, uploader.uploaded)
	}

	refs, err := st.ListItemMediaRefs(context.Background(), itemID)
	if err != nil {
		t.Fatalf("ListItemMediaRefs: %v", err)
	}
	if len(refs) != 1 {
		t.Fatalf("expected one media ref, got %#v", refs)
	}
	if refs[0].LocalPrunedAt.IsZero() {
		t.Fatalf("expected local pruned timestamp, got %+v", refs[0])
	}
	assets, err := st.ListMediaAssetsByLocalPath(context.Background(), localPath)
	if err != nil {
		t.Fatalf("ListMediaAssetsByLocalPath: %v", err)
	}
	if len(assets) != 1 || assets[0].ArchiveETag != "etag:"+localPath {
		t.Fatalf("expected archive etag to be stored, got %#v", assets)
	}
	if _, err := os.Stat(filepath.Join(cfg.VaultDir, filepath.FromSlash(localPath))); !os.IsNotExist(err) {
		t.Fatalf("expected pruned file to be removed, stat err=%v", err)
	}
}

func setupArchivedPhotoItem(t *testing.T, sourceKey string, remoteURL string, ocrReady bool) (config.Config, *store.Store, int64, string) {
	t.Helper()

	cfg, err := config.Load(t.TempDir())
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if err := cfg.EnsureDirs(); err != nil {
		t.Fatalf("EnsureDirs: %v", err)
	}

	st, err := store.Open(cfg.DBPath)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() {
		_ = st.Close()
	})

	itemID, _, localPath, _ := setupArchivedPhotoItemWithSharedPath(t, cfg, st, sourceKey, remoteURL, "", ocrReady)
	return cfg, st, itemID, localPath
}

func setupArchivedPhotoItemWithSharedPath(t *testing.T, cfg config.Config, st *store.Store, sourceKey string, remoteURL string, sharedLocalPath string, ocrReady bool) (int64, int64, string, string) {
	t.Helper()

	now := time.Date(2026, 4, 25, 20, 0, 0, 0, time.UTC)
	item, err := st.UpsertItem(context.Background(), model.Item{
		SourceKey:    sourceKey,
		SourceType:   "x_bookmark",
		ExternalID:   strings.TrimPrefix(sourceKey, "x:"),
		CanonicalURL: "https://x.com/example/status/" + strings.TrimPrefix(sourceKey, "x:"),
		Title:        sourceKey,
		ContentHash:  sourceKey + "-hash",
		LinksJSON:    "[]",
		NotePath:     "items/x/2026/" + strings.TrimPrefix(sourceKey, "x:") + ".md",
		RawJSON:      `{}`,
		ImportedAt:   now,
		UpdatedAt:    now,
		LastSeenAt:   now,
	})
	if err != nil {
		t.Fatalf("UpsertItem: %v", err)
	}

	if _, err := st.SaveXHydration(context.Background(), item.ItemID, model.XHydration{
		FullText:  "hello",
		Language:  "en",
		Status:    "ok_graphql",
		FetchedAt: now,
		APIJSON:   `{"snapshot":{"media_objects":[{"type":"photo","url":"` + remoteURL + `","expanded_url":"https://x.com/example/status/123/photo/1","width":1200,"height":800}]}}`,
	}); err != nil {
		t.Fatalf("SaveXHydration: %v", err)
	}

	refs, err := st.ListItemMediaRefs(context.Background(), item.ItemID)
	if err != nil {
		t.Fatalf("ListItemMediaRefs: %v", err)
	}
	if len(refs) != 1 {
		t.Fatalf("expected one media ref, got %#v", refs)
	}

	localPath := sharedLocalPath
	if localPath == "" {
		localPath = "media/x/photo/ab/" + strings.TrimPrefix(sourceKey, "x:") + ".jpg"
	}
	fullPath := cfg.VaultDir + "/" + localPath
	if err := os.MkdirAll(filepath.Dir(fullPath), 0o755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	if err := os.WriteFile(fullPath, []byte("photo-bytes"), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	if _, err := st.SaveMediaDownload(context.Background(), refs[0].MediaAssetID, model.MediaDownloadResult{
		MIMEType:     "image/jpeg",
		ByteSize:     int64(len("photo-bytes")),
		ContentHash:  "sha256:" + strings.TrimPrefix(sourceKey, "x:"),
		LocalPath:    localPath,
		Status:       "downloaded",
		DownloadedAt: now,
	}); err != nil {
		t.Fatalf("SaveMediaDownload: %v", err)
	}

	if ocrReady {
		if _, err := st.SaveItemOCR(context.Background(), item.ItemID, model.OCRResult{
			Text:        "photo text",
			Status:      "ok",
			Model:       "test/ocr",
			Tool:        "test-tool",
			ToolVersion: "v1",
			FetchedAt:   now,
		}, "input-hash"); err != nil {
			t.Fatalf("SaveItemOCR: %v", err)
		}
	}

	return item.ItemID, refs[0].MediaAssetID, localPath, fullPath
}
