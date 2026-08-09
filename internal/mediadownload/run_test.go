package mediadownload

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/darron/dbrain/internal/config"
	"github.com/darron/dbrain/internal/model"
	"github.com/darron/dbrain/internal/safehttp"
	"github.com/darron/dbrain/internal/store"
)

func TestDownloadRefClosesOriginalResponseBody(t *testing.T) {
	t.Parallel()

	cfg, err := config.Load(t.TempDir())
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if err := cfg.EnsureDirs(); err != nil {
		t.Fatalf("EnsureDirs: %v", err)
	}
	body := &trackingReadCloser{Reader: strings.NewReader("jpeg-bytes")}
	client := &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
		return &http.Response{
			StatusCode:    http.StatusOK,
			Header:        http.Header{"content-type": []string{"image/jpeg"}},
			Body:          body,
			ContentLength: int64(len("jpeg-bytes")),
		}, nil
	})}

	result, err := downloadRef(context.Background(), client, cfg, model.ItemMediaRef{
		RemoteURL: "https://media.example/image.jpg",
		MediaType: "photo",
	}, "x", progressOptions{})
	if err != nil {
		t.Fatalf("downloadRef: %v", err)
	}
	if result.Status != model.MediaDownloadStatusDownloaded {
		t.Fatalf("result = %#v", result)
	}
	if !body.closed {
		t.Fatal("downloadRef did not close the original response body")
	}
}

func TestMediaNamespaceForSourceType(t *testing.T) {
	for _, test := range []struct {
		sourceType string
		want       string
	}{
		{sourceType: "x_bookmark", want: "x"},
		{sourceType: "x_quote", want: "x"},
		{sourceType: "bsky_bookmark", want: "bsky"},
		{sourceType: "bsky_quote", want: "bsky"},
	} {
		t.Run(test.sourceType, func(t *testing.T) {
			if got := MediaNamespaceForSourceType(test.sourceType); got != test.want {
				t.Fatalf("MediaNamespaceForSourceType(%q) = %q, want %q", test.sourceType, got, test.want)
			}
		})
	}
}

type trackingReadCloser struct {
	io.Reader
	closed bool
}

func (b *trackingReadCloser) Close() error {
	b.closed = true
	return nil
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(req *http.Request) (*http.Response, error) {
	return f(req)
}

func TestRunForItemBlocksPrivateMediaWithoutWritingFile(t *testing.T) {
	hits := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		hits++
		w.Header().Set("content-type", "image/jpeg")
		_, _ = w.Write([]byte("private bytes"))
	}))
	defer server.Close()

	cfg, err := config.Load(t.TempDir())
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if err := cfg.EnsureDirs(); err != nil {
		t.Fatalf("EnsureDirs: %v", err)
	}
	st := openTestStore(t, cfg.DBPath)
	ctx := context.Background()
	now := time.Date(2026, 7, 12, 18, 0, 0, 0, time.UTC)
	itemID := insertTestItem(t, st, "x:block-private-media", now)
	if _, err := st.SaveXHydration(ctx, itemID, model.XHydration{
		FullText:  "private media",
		Status:    "ok_graphql",
		FetchedAt: now,
		APIJSON:   `{"snapshot":{"media_objects":[{"type":"photo","url":"` + server.URL + `/image.jpg"}]}}`,
	}); err != nil {
		t.Fatalf("SaveXHydration: %v", err)
	}

	stats, err := RunForItem(ctx, cfg, st, itemID, Options{})
	if err != nil {
		t.Fatalf("RunForItem: %v", err)
	}
	assertBlockedMediaResult(t, st, itemID, stats)
	if hits != 0 {
		t.Fatalf("private server hits = %d, want 0", hits)
	}
	assertNoMediaFiles(t, cfg.MediaDir)
}

func TestRunForItemBlocksRedirectFromPublicToPrivateWithoutWritingFile(t *testing.T) {
	hits := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits++
		if r.Host == "public.test" {
			http.Redirect(w, r, "http://private.test/media.jpg", http.StatusFound)
			return
		}
		w.Header().Set("content-type", "image/jpeg")
		_, _ = w.Write([]byte("private redirected bytes"))
	}))
	defer server.Close()

	cfg, err := config.Load(t.TempDir())
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if err := cfg.EnsureDirs(); err != nil {
		t.Fatalf("EnsureDirs: %v", err)
	}
	st := openTestStore(t, cfg.DBPath)
	ctx := context.Background()
	now := time.Date(2026, 7, 12, 18, 5, 0, 0, time.UTC)
	itemID := insertTestItem(t, st, "x:block-private-redirect-media", now)
	if _, err := st.SaveXHydration(ctx, itemID, model.XHydration{
		FullText:  "redirected private media",
		Status:    "ok_graphql",
		FetchedAt: now,
		APIJSON:   `{"snapshot":{"media_objects":[{"type":"photo","url":"http://public.test/image.jpg"}]}}`,
	}); err != nil {
		t.Fatalf("SaveXHydration: %v", err)
	}

	policy := safehttp.Policy{
		LookupNetIP: func(_ context.Context, _ string, host string) ([]netip.Addr, error) {
			if host == "public.test" {
				return []netip.Addr{netip.MustParseAddr("8.8.8.8")}, nil
			}
			return []netip.Addr{netip.MustParseAddr("127.0.0.1")}, nil
		},
		DialContext: func(ctx context.Context, network, _ string) (net.Conn, error) {
			var dialer net.Dialer
			return dialer.DialContext(ctx, network, server.Listener.Addr().String())
		},
	}
	stats, err := RunForItem(ctx, cfg, st, itemID, Options{httpPolicy: &policy})
	if err != nil {
		t.Fatalf("RunForItem: %v", err)
	}
	assertBlockedMediaResult(t, st, itemID, stats)
	if hits != 1 {
		t.Fatalf("server hits = %d, want only initial public request", hits)
	}
	assertNoMediaFiles(t, cfg.MediaDir)
}

func assertBlockedMediaResult(t *testing.T, st *store.Store, itemID int64, stats Stats) {
	t.Helper()
	if stats.Blocked != 1 || stats.Errors != 0 || stats.Downloaded != 0 {
		t.Fatalf("unexpected policy-blocked stats: %+v", stats)
	}
	refs, err := st.ListItemMediaRefs(context.Background(), itemID)
	if err != nil {
		t.Fatalf("ListItemMediaRefs: %v", err)
	}
	if len(refs) != 1 || refs[0].DownloadStatus != model.MediaDownloadStatusBlocked || refs[0].LocalPath != "" {
		t.Fatalf("unexpected blocked media ref: %+v", refs)
	}
}

func assertNoMediaFiles(t *testing.T, root string) {
	t.Helper()
	err := filepath.WalkDir(root, func(path string, entry os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if path != root && !entry.IsDir() {
			t.Fatalf("unexpected media file after policy rejection: %s", path)
		}
		return nil
	})
	if err != nil {
		t.Fatalf("inspect media directory: %v", err)
	}
}

func TestRunForItemDownloadsMediaIntoVault(t *testing.T) {
	t.Parallel()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("content-type", "image/jpeg")
		_, _ = w.Write([]byte("jpeg-bytes"))
	}))
	defer server.Close()
	publicMediaURL := strings.Replace(server.URL, "127.0.0.1", "media.test", 1) + "/image.jpg"

	cfg, err := config.Load(t.TempDir())
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if err := cfg.EnsureDirs(); err != nil {
		t.Fatalf("EnsureDirs: %v", err)
	}

	st := openTestStore(t, cfg.DBPath)
	ctx := context.Background()
	now := time.Date(2026, 4, 22, 3, 4, 5, 0, time.UTC)

	itemID := insertTestItem(t, st, "x:download-media", now)
	changed, err := st.SaveXHydration(ctx, itemID, model.XHydration{
		FullText:  "hello",
		Language:  "en",
		Status:    "ok_graphql",
		FetchedAt: now,
		APIJSON:   `{"snapshot":{"media_objects":[{"type":"photo","url":"` + publicMediaURL + `","expanded_url":"https://x.com/example/status/123/photo/1","width":1200,"height":800}]}}`,
	})
	if err != nil {
		t.Fatalf("SaveXHydration: %v", err)
	}
	if !changed {
		t.Fatal("expected hydration insert to change state")
	}

	policy := syntheticPublicMediaPolicy(server.Listener.Addr().String())
	stats, err := RunForItem(ctx, cfg, st, itemID, Options{httpPolicy: &policy})
	if err != nil {
		t.Fatalf("RunForItem: %v", err)
	}
	if stats.Candidates != 1 || stats.Requested != 1 || stats.Downloaded != 1 {
		t.Fatalf("unexpected download stats: %+v", stats)
	}

	refs, err := st.ListItemMediaRefs(ctx, itemID)
	if err != nil {
		t.Fatalf("ListItemMediaRefs: %v", err)
	}
	if len(refs) != 1 {
		t.Fatalf("expected one media ref, got %#v", refs)
	}
	if refs[0].DownloadStatus != "downloaded" {
		t.Fatalf("expected downloaded media ref, got %+v", refs[0])
	}
	if !strings.HasPrefix(refs[0].LocalPath, "media/x/photo/") {
		t.Fatalf("unexpected local path: %+v", refs[0])
	}
	fullPath := filepath.Join(cfg.VaultDir, filepath.FromSlash(refs[0].LocalPath))
	if _, err := os.Stat(fullPath); err != nil {
		t.Fatalf("expected downloaded file at %s: %v", fullPath, err)
	}
}

func TestRunForItemRejectsHLSPlaylistAsVideoAsset(t *testing.T) {
	t.Parallel()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("content-type", "application/vnd.apple.mpegurl")
		_, _ = w.Write([]byte("#EXTM3U\n#EXT-X-TARGETDURATION:6\n"))
	}))
	defer server.Close()

	cfg, err := config.Load(t.TempDir())
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if err := cfg.EnsureDirs(); err != nil {
		t.Fatalf("EnsureDirs: %v", err)
	}
	st := openTestStore(t, cfg.DBPath)
	ctx := context.Background()
	now := time.Date(2026, 8, 8, 3, 4, 5, 0, time.UTC)
	itemID := insertTestItem(t, st, "bsky:hls-playlist", now)
	if _, err := st.SaveXHydration(ctx, itemID, model.XHydration{
		FullText: "video", Status: "ok_graphql", FetchedAt: now,
		APIJSON: `{"snapshot":{"media_objects":[{"type":"video","url":"` + server.URL + `/playlist.m3u8"}]}}`,
	}); err != nil {
		t.Fatalf("SaveXHydration: %v", err)
	}

	stats, err := RunForItem(ctx, cfg, st, itemID, Options{httpPolicy: privateNetworkTestPolicy()})
	if err != nil {
		t.Fatalf("RunForItem: %v", err)
	}
	if stats.Errors != 1 || stats.Downloaded != 0 {
		t.Fatalf("unexpected playlist stats: %+v", stats)
	}
	refs, err := st.ListItemMediaRefs(ctx, itemID)
	if err != nil {
		t.Fatalf("ListItemMediaRefs: %v", err)
	}
	if len(refs) != 1 || refs[0].DownloadStatus != model.MediaDownloadStatusError || refs[0].LocalPath != "" {
		t.Fatalf("playlist ref = %#v", refs)
	}
	assertNoMediaFiles(t, cfg.MediaDir)
}

func TestRunForItemUsesBlueskyMediaNamespace(t *testing.T) {
	t.Parallel()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("content-type", "image/jpeg")
		_, _ = w.Write([]byte("bsky-image"))
	}))
	defer server.Close()

	cfg, err := config.Load(t.TempDir())
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if err := cfg.EnsureDirs(); err != nil {
		t.Fatalf("EnsureDirs: %v", err)
	}
	st := openTestStore(t, cfg.DBPath)
	ctx := context.Background()
	now := time.Date(2026, 8, 8, 3, 5, 5, 0, time.UTC)
	item, err := st.UpsertItem(ctx, model.Item{
		SourceKey: "bsky:namespace", SourceType: "bsky_bookmark", ExternalID: "bsky:namespace",
		CanonicalURL: "https://bsky.app/profile/alice.example/post/namespace", Title: "image",
		ContentHash: "bsky-namespace", LinksJSON: "[]", NotePath: "items/bsky/2026/namespace.md", RawJSON: "{}",
		ImportedAt: now, UpdatedAt: now, LastSeenAt: now,
	})
	if err != nil {
		t.Fatalf("UpsertItem: %v", err)
	}
	itemID := item.ItemID
	if _, err := st.SaveItemMediaCandidates(ctx, itemID, []model.MediaCandidate{{RemoteURL: server.URL + "/image.jpg", MediaType: "photo"}}); err != nil {
		t.Fatalf("SaveItemMediaCandidates: %v", err)
	}

	stats, err := RunForItem(ctx, cfg, st, itemID, Options{MediaNamespace: "bsky", httpPolicy: privateNetworkTestPolicy()})
	if err != nil {
		t.Fatalf("RunForItem: %v", err)
	}
	if stats.Downloaded != 1 {
		t.Fatalf("unexpected namespace stats: %+v", stats)
	}
	refs, err := st.ListItemMediaRefs(ctx, itemID)
	if err != nil {
		t.Fatalf("ListItemMediaRefs: %v", err)
	}
	if len(refs) != 1 || !strings.HasPrefix(refs[0].LocalPath, "media/bsky/photo/") {
		t.Fatalf("unexpected Bluesky local path: %#v", refs)
	}
}

func TestRunForItemStoresHeaderlessBlueskyVideoAsMP4(t *testing.T) {
	t.Parallel()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		// Minimal MP4 signature: ftyp immediately follows the box size.
		_, _ = w.Write([]byte{0, 0, 0, 24, 'f', 't', 'y', 'p', 'i', 's', 'o', 'm', 0, 0, 0, 0, 'i', 's', 'o', 'm', 'i', 's', 'o', '2', 0})
	}))
	defer server.Close()

	cfg, err := config.Load(t.TempDir())
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if err := cfg.EnsureDirs(); err != nil {
		t.Fatalf("EnsureDirs: %v", err)
	}
	st := openTestStore(t, cfg.DBPath)
	ctx := context.Background()
	now := time.Date(2026, 8, 8, 3, 6, 5, 0, time.UTC)
	itemID := insertTestItem(t, st, "bsky:headerless-video", now)
	if _, err := st.SaveItemMediaCandidates(ctx, itemID, []model.MediaCandidate{{RemoteURL: server.URL + "/xrpc/com.atproto.sync.getBlob?did=did%3Aplc%3Aone&cid=bafy-video", MediaType: "video"}}); err != nil {
		t.Fatalf("SaveItemMediaCandidates: %v", err)
	}

	stats, err := RunForItem(ctx, cfg, st, itemID, Options{MediaNamespace: "bsky", HTTPPolicy: privateNetworkTestPolicy()})
	if err != nil {
		t.Fatalf("RunForItem: %v", err)
	}
	if stats.Downloaded != 1 {
		t.Fatalf("unexpected video stats: %+v", stats)
	}
	refs, err := st.ListItemMediaRefs(ctx, itemID)
	if err != nil {
		t.Fatalf("ListItemMediaRefs: %v", err)
	}
	if len(refs) != 1 || !strings.HasSuffix(refs[0].LocalPath, ".mp4") || !strings.HasPrefix(refs[0].LocalPath, "media/bsky/video/") {
		t.Fatalf("unexpected headerless video ref: %#v", refs)
	}
	asset, err := st.GetMediaAsset(ctx, refs[0].MediaAssetID)
	if err != nil {
		t.Fatalf("GetMediaAsset: %v", err)
	}
	if asset.ByteSize == 0 || asset.ContentHash == "" {
		t.Fatalf("headerless video asset lacks bytes/hash: %#v", asset)
	}
}

func TestRunForItemAssetAllowlistFiltersRefsAndCandidateCount(t *testing.T) {
	t.Parallel()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("content-type", "image/jpeg")
		_, _ = w.Write([]byte("jpeg-" + r.URL.Path))
	}))
	defer server.Close()

	cfg, err := config.Load(t.TempDir())
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if err := cfg.EnsureDirs(); err != nil {
		t.Fatalf("EnsureDirs: %v", err)
	}
	st := openTestStore(t, cfg.DBPath)
	ctx := context.Background()
	now := time.Date(2026, 7, 15, 19, 0, 0, 0, time.UTC)
	itemID := insertTestItem(t, st, "x:allowlisted-media", now)
	if _, err := st.SaveXHydration(ctx, itemID, model.XHydration{
		FullText: "two photos", Status: "ok_graphql", FetchedAt: now,
		APIJSON: `{"snapshot":{"media_objects":[` +
			`{"type":"photo","url":"` + server.URL + `/excluded.jpg"},` +
			`{"type":"photo","url":"` + server.URL + `/allowed.jpg"}` +
			`]}}`,
	}); err != nil {
		t.Fatalf("SaveXHydration: %v", err)
	}
	refs, err := st.ListItemMediaRefs(ctx, itemID)
	if err != nil || len(refs) != 2 {
		t.Fatalf("ListItemMediaRefs: refs=%+v err=%v", refs, err)
	}
	allowedID := refs[1].MediaAssetID
	stats, err := RunForItem(ctx, cfg, st, itemID, Options{
		Force: true, AllowedAssetIDs: []int64{allowedID}, httpPolicy: privateNetworkTestPolicy(),
	})
	if err != nil {
		t.Fatalf("RunForItem: %v", err)
	}
	if stats.Candidates != 1 || stats.Requested != 1 || stats.Downloaded != 1 {
		t.Fatalf("allowlist did not bound stats/work: %+v", stats)
	}
	refs, err = st.ListItemMediaRefs(ctx, itemID)
	if err != nil {
		t.Fatalf("ListItemMediaRefs after: %v", err)
	}
	for _, ref := range refs {
		if ref.MediaAssetID == allowedID && ref.DownloadStatus != model.MediaDownloadStatusDownloaded {
			t.Fatalf("allowed ref was not downloaded: %+v", ref)
		}
		if ref.MediaAssetID != allowedID && ref.DownloadStatus != model.MediaDownloadStatusPending {
			t.Fatalf("excluded ref was modified: %+v", ref)
		}
	}
}

func TestRunForItemLogsLargeDownloadProgress(t *testing.T) {
	t.Parallel()

	payload := []byte("progress-bytes")
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("content-type", "video/mp4")
		w.Header().Set("content-length", strconv.Itoa(len(payload)))
		_, _ = w.Write(payload)
	}))
	defer server.Close()

	cfg, err := config.Load(t.TempDir())
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if err := cfg.EnsureDirs(); err != nil {
		t.Fatalf("EnsureDirs: %v", err)
	}

	st := openTestStore(t, cfg.DBPath)
	ctx := context.Background()
	now := time.Date(2026, 5, 5, 6, 0, 0, 0, time.UTC)

	itemID := insertTestItem(t, st, "x:download-progress", now)
	if _, err := st.SaveXHydration(ctx, itemID, model.XHydration{
		FullText:  "video",
		Language:  "en",
		Status:    "ok_graphql",
		FetchedAt: now,
		APIJSON:   `{"snapshot":{"media_objects":[{"type":"video","url":"` + server.URL + `/video.mp4","expanded_url":"https://x.com/example/status/123/video/1","width":3840,"height":2160}]}}`,
	}); err != nil {
		t.Fatalf("SaveXHydration: %v", err)
	}

	var logs bytes.Buffer
	logger := slog.New(slog.NewTextHandler(&logs, &slog.HandlerOptions{Level: slog.LevelDebug}))
	stats, err := RunForItem(ctx, cfg, st, itemID, Options{
		Logger:           logger,
		ProgressBytes:    1,
		ProgressInterval: time.Hour,
		httpPolicy:       privateNetworkTestPolicy(),
	})
	if err != nil {
		t.Fatalf("RunForItem: %v", err)
	}
	if stats.Downloaded != 1 {
		t.Fatalf("expected downloaded media, got %+v", stats)
	}

	logOutput := logs.String()
	for _, value := range []string{"x media download started", "x media download progress", "media_asset_id", "percent"} {
		if !strings.Contains(logOutput, value) {
			t.Fatalf("expected progress log to contain %q, got %q", value, logOutput)
		}
	}
}

func TestRunForItemMarksGoneMedia(t *testing.T) {
	t.Parallel()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.NotFound(w, r)
	}))
	defer server.Close()

	cfg, err := config.Load(t.TempDir())
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if err := cfg.EnsureDirs(); err != nil {
		t.Fatalf("EnsureDirs: %v", err)
	}

	st := openTestStore(t, cfg.DBPath)
	ctx := context.Background()
	now := time.Date(2026, 4, 22, 3, 4, 5, 0, time.UTC)

	itemID := insertTestItem(t, st, "x:gone-media", now)
	if _, err := st.SaveXHydration(ctx, itemID, model.XHydration{
		FullText:  "hello",
		Language:  "en",
		Status:    "ok_graphql",
		FetchedAt: now,
		APIJSON:   `{"snapshot":{"media_objects":[{"type":"photo","url":"` + server.URL + `/missing.jpg","expanded_url":"https://x.com/example/status/123/photo/1","width":1200,"height":800}]}}`,
	}); err != nil {
		t.Fatalf("SaveXHydration: %v", err)
	}

	stats, err := RunForItem(ctx, cfg, st, itemID, Options{httpPolicy: privateNetworkTestPolicy()})
	if err != nil {
		t.Fatalf("RunForItem: %v", err)
	}
	if stats.Gone != 1 {
		t.Fatalf("expected gone media stat, got %+v", stats)
	}

	refs, err := st.ListItemMediaRefs(ctx, itemID)
	if err != nil {
		t.Fatalf("ListItemMediaRefs: %v", err)
	}
	if len(refs) != 1 {
		t.Fatalf("expected one media ref, got %#v", refs)
	}
	if refs[0].DownloadStatus != "gone" {
		t.Fatalf("expected gone media ref, got %+v", refs[0])
	}
	if refs[0].LocalPath != "" {
		t.Fatalf("expected gone media to have no local path, got %+v", refs[0])
	}
}

func TestRunForItemBlocksMediaAfterRepeatedErrors(t *testing.T) {
	t.Parallel()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "busy", http.StatusInternalServerError)
	}))
	defer server.Close()

	cfg, err := config.Load(t.TempDir())
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if err := cfg.EnsureDirs(); err != nil {
		t.Fatalf("EnsureDirs: %v", err)
	}

	st := openTestStore(t, cfg.DBPath)
	ctx := context.Background()
	now := time.Date(2026, 5, 5, 5, 14, 1, 0, time.UTC)

	itemID := insertTestItem(t, st, "x:block-error-media", now)
	if _, err := st.SaveXHydration(ctx, itemID, model.XHydration{
		FullText:  "hello",
		Language:  "en",
		Status:    "ok_graphql",
		FetchedAt: now,
		APIJSON:   `{"snapshot":{"media_objects":[{"type":"video","url":"` + server.URL + `/video.mp4","expanded_url":"https://x.com/example/status/123/video/1","width":1920,"height":1080}]}}`,
	}); err != nil {
		t.Fatalf("SaveXHydration: %v", err)
	}

	var stats Stats
	for range model.MediaDownloadMaxConsecutiveErrors {
		stats, err = RunForItem(ctx, cfg, st, itemID, Options{Force: true, httpPolicy: privateNetworkTestPolicy()})
		if err != nil {
			t.Fatalf("RunForItem: %v", err)
		}
	}
	if stats.Blocked != 1 {
		t.Fatalf("expected terminal blocked stat on final attempt, got %+v", stats)
	}

	refs, err := st.ListItemMediaRefs(ctx, itemID)
	if err != nil {
		t.Fatalf("ListItemMediaRefs: %v", err)
	}
	if len(refs) != 1 {
		t.Fatalf("expected one media ref, got %#v", refs)
	}
	if refs[0].DownloadStatus != "blocked" {
		t.Fatalf("expected blocked media ref, got %+v", refs[0])
	}
	if refs[0].DownloadErrors != model.MediaDownloadMaxConsecutiveErrors {
		t.Fatalf("expected %d errors, got %+v", model.MediaDownloadMaxConsecutiveErrors, refs[0])
	}
}

func TestShouldDownloadSkipsArchivedPrunedMedia(t *testing.T) {
	t.Parallel()

	cfg, err := config.Load(t.TempDir())
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if err := cfg.EnsureDirs(); err != nil {
		t.Fatalf("EnsureDirs: %v", err)
	}

	ref := model.ItemMediaRef{
		DownloadStatus: "downloaded",
		LocalPath:      "media/x/photo/ab/test.jpg",
		ArchiveStatus:  "archived",
		LocalPrunedAt:  time.Now().UTC(),
	}
	if shouldDownload(ref, cfg, false) {
		t.Fatal("expected archived pruned media to skip re-download")
	}
}

func TestShouldDownloadBacksOffRecentErrors(t *testing.T) {
	t.Parallel()

	cfg, err := config.Load(t.TempDir())
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if err := cfg.EnsureDirs(); err != nil {
		t.Fatalf("EnsureDirs: %v", err)
	}

	now := time.Now().UTC()
	recent := model.ItemMediaRef{
		DownloadStatus: "error",
		DownloadErrors: 1,
		LastDownloadAt: now,
	}
	if shouldDownload(recent, cfg, false) {
		t.Fatal("expected recent media error to wait for retry cooldown")
	}

	old := recent
	old.LastDownloadAt = now.Add(-model.MediaDownloadRetryCooldown - time.Minute)
	if !shouldDownload(old, cfg, false) {
		t.Fatal("expected old media error to retry after cooldown")
	}

	blocked := old
	blocked.DownloadErrors = model.MediaDownloadMaxConsecutiveErrors
	if shouldDownload(blocked, cfg, false) {
		t.Fatal("expected terminal media errors to stay out of retry queue")
	}
}

func openTestStore(t *testing.T, path string) *store.Store {
	t.Helper()

	st, err := store.Open(path)
	if err != nil {
		t.Fatalf("open test store: %v", err)
	}
	t.Cleanup(func() {
		_ = st.Close()
	})
	return st
}

func insertTestItem(t *testing.T, st *store.Store, sourceKey string, now time.Time) int64 {
	t.Helper()

	result, err := st.UpsertItem(context.Background(), model.Item{
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
	return result.ItemID
}

func privateNetworkTestPolicy() *safehttp.Policy {
	return &safehttp.Policy{AllowPrivateNetwork: true}
}

func syntheticPublicMediaPolicy(serverAddress string) safehttp.Policy {
	return safehttp.Policy{
		LookupNetIP: func(_ context.Context, _ string, host string) ([]netip.Addr, error) {
			if host != "media.test" {
				return nil, fmt.Errorf("unexpected host %q", host)
			}
			return []netip.Addr{netip.MustParseAddr("8.8.8.8")}, nil
		},
		DialContext: func(ctx context.Context, network, _ string) (net.Conn, error) {
			var dialer net.Dialer
			return dialer.DialContext(ctx, network, serverAddress)
		},
	}
}
