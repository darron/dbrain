package mediadownload

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/binary"
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

func TestDownloadRefAcceptsProductionMastodonImageResponses(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name        string
		fixturePath string
		contentType string
		byteSize    int64
		digest      string
		extension   string
	}{
		{
			name:        "rgba png",
			fixturePath: "testdata/mastodon-image-rgba.png",
			contentType: "image/png",
			byteSize:    1111389,
			digest:      "fdcb51f8e12df2a92f00f406a3830a989843b7b23ab2823f3a2625ca38ad25a2",
			extension:   ".png",
		},
		{
			name:        "jfif jpeg",
			fixturePath: "testdata/mastodon-image-jfif.jpg",
			contentType: "image/jpeg",
			byteSize:    62843,
			digest:      "d2a5f8641ddc89a3247f7516e4e104b4493f37be15d5cb134b6ae1e454ff0ca5",
			extension:   ".jpg",
		},
	}

	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			body, err := os.ReadFile(test.fixturePath)
			if err != nil {
				t.Fatalf("read fixture: %v", err)
			}
			if got := fmt.Sprintf("%x", sha256.Sum256(body)); got != test.digest {
				t.Fatalf("fixture sha256 = %s, want %s", got, test.digest)
			}
			cfg, err := config.Load(t.TempDir())
			if err != nil {
				t.Fatalf("Load: %v", err)
			}
			if err := cfg.EnsureDirs(); err != nil {
				t.Fatalf("EnsureDirs: %v", err)
			}
			client := &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
				return &http.Response{
					StatusCode:    http.StatusOK,
					Header:        http.Header{"Content-Type": []string{test.contentType}},
					Body:          io.NopCloser(bytes.NewReader(body)),
					ContentLength: int64(len(body)),
				}, nil
			})}

			result, err := downloadRef(context.Background(), client, cfg, model.ItemMediaRef{
				RemoteURL: "https://media.example/fixture" + test.extension,
				MediaType: "photo",
			}, "mastodon", progressOptions{})
			if err != nil {
				t.Fatalf("downloadRef: %v", err)
			}
			if result.Status != model.MediaDownloadStatusDownloaded ||
				result.MIMEType != test.contentType ||
				result.ByteSize != test.byteSize ||
				result.ContentHash != "sha256:"+test.digest {
				t.Fatalf("result = %#v", result)
			}
			wantPath := filepath.ToSlash(filepath.Join("media", "mastodon", "photo", test.digest[:2], test.digest+test.extension))
			if result.LocalPath != wantPath {
				t.Fatalf("local path = %q, want %q", result.LocalPath, wantPath)
			}
			persisted, err := os.ReadFile(filepath.Join(cfg.VaultDir, filepath.FromSlash(result.LocalPath)))
			if err != nil {
				t.Fatalf("read persisted media: %v", err)
			}
			if !bytes.Equal(persisted, body) {
				t.Fatal("persisted media bytes differ from complete response fixture")
			}
		})
	}
}

func TestDownloadRefClosesOriginalResponseBody(t *testing.T) {
	t.Parallel()

	cfg, err := config.Load(t.TempDir())
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if err := cfg.EnsureDirs(); err != nil {
		t.Fatalf("EnsureDirs: %v", err)
	}
	body := &trackingReadCloser{Reader: bytes.NewReader(fakeJPEGBytes())}
	client := &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
		return &http.Response{
			StatusCode:    http.StatusOK,
			Header:        http.Header{"content-type": []string{"image/jpeg"}},
			Body:          body,
			ContentLength: int64(len(fakeJPEGBytes())),
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

func TestDownloadRefRejectsChunkedBodyOverConfiguredLimit(t *testing.T) {
	cfg, err := config.Load(t.TempDir())
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if err := cfg.EnsureDirs(); err != nil {
		t.Fatalf("EnsureDirs: %v", err)
	}
	client := &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
		return &http.Response{
			StatusCode:    http.StatusOK,
			Header:        http.Header{"content-type": []string{"audio/mpeg"}},
			Body:          io.NopCloser(strings.NewReader("0123456789")),
			ContentLength: -1,
		}, nil
	})}
	result, err := downloadRef(context.Background(), client, cfg, model.ItemMediaRef{RemoteURL: "https://media.example/audio.mp3", MediaType: "audio"}, "mastodon", progressOptions{MaxBytes: 4})
	if err != nil {
		t.Fatalf("downloadRef: %v", err)
	}
	if result.Status != model.MediaDownloadStatusBlocked || !strings.Contains(result.Error, "exceeds 4") {
		t.Fatalf("result = %#v", result)
	}
	assertNoMediaFiles(t, cfg.MediaDir)
}

func TestDownloadRefRejectsDeclaredBodyOverConfiguredLimit(t *testing.T) {
	cfg, err := config.Load(t.TempDir())
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if err := cfg.EnsureDirs(); err != nil {
		t.Fatalf("EnsureDirs: %v", err)
	}
	client := &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
		return &http.Response{
			StatusCode:    http.StatusOK,
			Header:        http.Header{"content-type": []string{"image/jpeg"}},
			Body:          io.NopCloser(strings.NewReader("not read")),
			ContentLength: 5,
		}, nil
	})}
	result, err := downloadRef(context.Background(), client, cfg, model.ItemMediaRef{RemoteURL: "https://media.example/image.jpg", MediaType: "photo"}, "mastodon", progressOptions{MaxBytes: 4})
	if err != nil {
		t.Fatalf("downloadRef: %v", err)
	}
	if result.Status != model.MediaDownloadStatusBlocked || !strings.Contains(result.Error, "exceeds 4") {
		t.Fatalf("result = %#v", result)
	}
	assertNoMediaFiles(t, cfg.MediaDir)
}

func TestDownloadRefTreatsRequestTimeoutAsRetryable(t *testing.T) {
	cfg, err := config.Load(t.TempDir())
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if err := cfg.EnsureDirs(); err != nil {
		t.Fatalf("EnsureDirs: %v", err)
	}
	client := &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: http.StatusRequestTimeout, Body: io.NopCloser(strings.NewReader("timeout"))}, nil
	})}
	result, err := downloadRef(context.Background(), client, cfg, model.ItemMediaRef{RemoteURL: "https://media.example/image.jpg", MediaType: "photo"}, "mastodon", progressOptions{})
	if err != nil {
		t.Fatalf("downloadRef: %v", err)
	}
	if result.Status != model.MediaDownloadStatusError {
		t.Fatalf("result = %#v, want retryable error status", result)
	}
}

func TestDownloadRefPreservesRetryableTransportAndHTTPFailures(t *testing.T) {
	tests := []struct {
		name      string
		status    int
		transport error
	}{
		{name: "transport timeout", transport: context.DeadlineExceeded},
		{name: "rate limited", status: http.StatusTooManyRequests},
		{name: "internal server error", status: http.StatusInternalServerError},
		{name: "bad gateway", status: http.StatusBadGateway},
		{name: "service unavailable", status: http.StatusServiceUnavailable},
		{name: "gateway timeout", status: http.StatusGatewayTimeout},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			cfg, err := config.Load(t.TempDir())
			if err != nil {
				t.Fatalf("Load: %v", err)
			}
			if err := cfg.EnsureDirs(); err != nil {
				t.Fatalf("EnsureDirs: %v", err)
			}
			client := &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
				if test.transport != nil {
					return nil, test.transport
				}
				return &http.Response{StatusCode: test.status, Body: io.NopCloser(strings.NewReader("temporary failure"))}, nil
			})}
			result, err := downloadRef(context.Background(), client, cfg, model.ItemMediaRef{RemoteURL: "https://media.example/image.jpg", MediaType: "photo"}, "mastodon", progressOptions{})
			if err != nil {
				t.Fatalf("downloadRef: %v", err)
			}
			if result.Status != model.MediaDownloadStatusError {
				t.Fatalf("result = %#v, want retryable error", result)
			}
			assertNoMediaFiles(t, cfg.MediaDir)
		})
	}
}

func TestDownloadRefRejectsInvalidImageResponsesWithoutPromotion(t *testing.T) {
	pngBytes, err := os.ReadFile("testdata/mastodon-image-rgba.png")
	if err != nil {
		t.Fatalf("read PNG fixture: %v", err)
	}
	jpegBytes, err := os.ReadFile("testdata/mastodon-image-jfif.jpg")
	if err != nil {
		t.Fatalf("read JPEG fixture: %v", err)
	}
	tests := []struct {
		name        string
		contentType string
		body        []byte
	}{
		{name: "empty", contentType: "image/png", body: nil},
		{name: "truncated png", contentType: "image/png", body: pngBytes[:64]},
		{name: "truncated jpeg", contentType: "image/jpeg", body: jpegBytes[:len(jpegBytes)/2]},
		{name: "MIME disagreement", contentType: "image/jpeg", body: pngBytes},
		{name: "unsafe SVG", contentType: "image/svg+xml", body: []byte(`<svg xmlns="http://www.w3.org/2000/svg"></svg>`)},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			cfg, err := config.Load(t.TempDir())
			if err != nil {
				t.Fatalf("Load: %v", err)
			}
			if err := cfg.EnsureDirs(); err != nil {
				t.Fatalf("EnsureDirs: %v", err)
			}
			client := &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
				return &http.Response{
					StatusCode:    http.StatusOK,
					Header:        http.Header{"Content-Type": []string{test.contentType}},
					Body:          io.NopCloser(bytes.NewReader(test.body)),
					ContentLength: int64(len(test.body)),
				}, nil
			})}
			result, err := downloadRef(context.Background(), client, cfg, model.ItemMediaRef{RemoteURL: "https://media.example/image", MediaType: "photo"}, "mastodon", progressOptions{})
			if err != nil {
				t.Fatalf("downloadRef: %v", err)
			}
			if result.Status != model.MediaDownloadStatusBlocked || result.LocalPath != "" {
				t.Fatalf("result = %#v, want terminal blocked without promotion", result)
			}
			assertNoMediaFiles(t, cfg.MediaDir)
		})
	}
}

func TestDownloadRefRejectsMislabeledJSONAsPhoto(t *testing.T) {
	cfg, err := config.Load(t.TempDir())
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if err := cfg.EnsureDirs(); err != nil {
		t.Fatalf("EnsureDirs: %v", err)
	}
	client := &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
		return &http.Response{
			StatusCode: http.StatusOK,
			Header:     http.Header{"content-type": []string{"image/jpeg"}},
			Body:       io.NopCloser(strings.NewReader(`{"error":"not media"}`)),
		}, nil
	})}
	result, err := downloadRef(context.Background(), client, cfg, model.ItemMediaRef{RemoteURL: "https://media.example/image.jpg", MediaType: "photo"}, "mastodon", progressOptions{})
	if err != nil {
		t.Fatalf("downloadRef: %v", err)
	}
	if result.Status != model.MediaDownloadStatusBlocked || !strings.Contains(result.Error, "content sniffed") {
		t.Fatalf("result = %#v, want a content validation error", result)
	}
	assertNoMediaFiles(t, cfg.MediaDir)
}

func TestDownloadRefRejectsUnrecognizedVideoAndAudioBytes(t *testing.T) {
	for _, test := range []struct {
		name      string
		mediaType string
		mimeType  string
	}{
		{name: "video", mediaType: "video", mimeType: "video/mp4"},
		{name: "audio", mediaType: "audio", mimeType: "audio/mpeg"},
	} {
		t.Run(test.name, func(t *testing.T) {
			cfg, err := config.Load(t.TempDir())
			if err != nil {
				t.Fatalf("Load: %v", err)
			}
			if err := cfg.EnsureDirs(); err != nil {
				t.Fatalf("EnsureDirs: %v", err)
			}
			client := &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
				return &http.Response{
					StatusCode: http.StatusOK,
					Header:     http.Header{"content-type": []string{test.mimeType}},
					Body:       io.NopCloser(strings.NewReader("arbitrary binary bytes")),
				}, nil
			})}
			result, err := downloadRef(context.Background(), client, cfg, model.ItemMediaRef{RemoteURL: "https://media.example/content", MediaType: test.mediaType}, "mastodon", progressOptions{})
			if err != nil {
				t.Fatalf("downloadRef: %v", err)
			}
			if result.Status != model.MediaDownloadStatusBlocked || (!strings.Contains(result.Error, "content sniffed") && !strings.Contains(result.Error, "recognized")) {
				t.Fatalf("result = %#v, want format validation error", result)
			}
			assertNoMediaFiles(t, cfg.MediaDir)
		})
	}
}

func TestDownloadRefRejectsTruncatedContainerMagicForVideoAndAudio(t *testing.T) {
	for _, test := range []struct {
		name      string
		mediaType string
		mimeType  string
		body      []byte
	}{
		{
			name:      "truncated mp4 ftyp box",
			mediaType: "video",
			mimeType:  "video/mp4",
			body:      []byte{0, 0, 0, 0, 'f', 't', 'y', 'p', 'm', 'p', '4', '2'},
		},
		{
			name:      "truncated ogg page",
			mediaType: "audio",
			mimeType:  "audio/ogg",
			body:      []byte{'O', 'g', 'g', 'S'},
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			cfg, err := config.Load(t.TempDir())
			if err != nil {
				t.Fatalf("Load: %v", err)
			}
			if err := cfg.EnsureDirs(); err != nil {
				t.Fatalf("EnsureDirs: %v", err)
			}
			client := &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
				return &http.Response{
					StatusCode: http.StatusOK,
					Header:     http.Header{"content-type": []string{test.mimeType}},
					Body:       io.NopCloser(bytes.NewReader(test.body)),
				}, nil
			})}
			result, err := downloadRef(context.Background(), client, cfg, model.ItemMediaRef{RemoteURL: "https://media.example/content", MediaType: test.mediaType}, "mastodon", progressOptions{})
			if err != nil {
				t.Fatalf("downloadRef: %v", err)
			}
			if result.Status != model.MediaDownloadStatusBlocked {
				t.Fatalf("result = %#v, want a container validation error", result)
			}
			assertNoMediaFiles(t, cfg.MediaDir)
		})
	}
}

func TestValidateMediaFileRejectsHeaderOnlyOrGarbageForEveryAcceptedContainer(t *testing.T) {
	wav := []byte{'R', 'I', 'F', 'F', 0, 0, 0, 0, 'W', 'A', 'V', 'E', 'f', 'm', 't', ' ', 16, 0, 0, 0, 1, 0, 1, 0, 0x44, 0xac, 0, 0, 0x88, 0x58, 1, 0, 2, 0, 16, 0, 'd', 'a', 't', 'a', 2, 0, 0, 0, 0, 0}
	binary.LittleEndian.PutUint32(wav[4:8], uint32(len(wav)-8))
	flac := append([]byte("fLaC"), []byte{0x80, 0, 0, 34}...)
	flac = append(flac, make([]byte, 34)...)
	flac = append(flac, 0xff, 0xf8, 0, 0)
	mp3 := append([]byte{0xff, 0xfb, 0x90, 0x64}, bytes.Repeat([]byte{0}, 413)...)
	cases := []struct {
		name, expected, declared string
		valid, invalid           []byte
	}{
		{name: "mp4 video", expected: "video", declared: "video/mp4", valid: genuineMP4VideoBytes(), invalid: withGarbage(genuineMP4VideoBytes()[:24])},
		{name: "mp4 audio", expected: "audio", declared: "audio/mp4", valid: genuineMP4AudioBytes(), invalid: withGarbage(genuineMP4AudioBytes()[:24])},
		{name: "webm video", expected: "video", declared: "video/webm", valid: genuineWebMVideoBytes(), invalid: withGarbage(genuineWebMVideoBytes()[:12])},
		{name: "webm audio", expected: "audio", declared: "audio/webm", valid: genuineWebMAudioBytes(), invalid: withGarbage(genuineWebMAudioBytes()[:12])},
		{name: "ogg audio", expected: "audio", declared: "audio/ogg", valid: genuineOggAudioBytes(), invalid: withGarbage(genuineOggAudioBytes()[:4])},
		{name: "mpeg ts", expected: "video", declared: "video/mp2t", valid: genuineMPEGTSVideoBytes(), invalid: withGarbage(genuineMPEGTSVideoBytes()[:4])},
		{name: "wav", expected: "audio", declared: "audio/wav", valid: wav, invalid: withGarbage(wav[:12])},
		{name: "flac", expected: "audio", declared: "audio/flac", valid: flac, invalid: withGarbage(flac[:4])},
		{name: "mp3", expected: "audio", declared: "audio/mpeg", valid: mp3, invalid: withGarbage(mp3[:4])},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			prefix := test.valid
			if len(prefix) > 512 {
				prefix = prefix[:512]
			}
			if err := validateMediaFile(test.expected, test.declared, prefix, bytes.NewReader(test.valid), int64(len(test.valid))); err != nil {
				t.Fatalf("valid fixture rejected: %v", err)
			}
			invalidPrefix := test.invalid
			if len(invalidPrefix) > 512 {
				invalidPrefix = invalidPrefix[:512]
			}
			if err := validateMediaFile(test.expected, test.declared, invalidPrefix, bytes.NewReader(test.invalid), int64(len(test.invalid))); err == nil {
				t.Fatal("header-only or garbage fixture was accepted")
			}
		})
	}
}

func TestValidateMediaFileRejectsSyntheticContainerShapes(t *testing.T) {
	ogg := make([]byte, 29)
	copy(ogg, []byte("OggS"))
	ogg[26] = 1
	ogg[27] = 1
	ogg[28] = 1
	ts := make([]byte, 188*4)
	for index := 0; index < 4; index++ {
		packet := ts[index*188 : (index+1)*188]
		packet[0], packet[1], packet[2], packet[3] = 0x47, 0, 1, 0x10
		for offset := 4; offset < len(packet); offset++ {
			packet[offset] = 0xaa
		}
	}
	cases := []struct {
		name, expected, declared string
		body                     []byte
	}{
		{name: "iso track plus arbitrary mdat", expected: "video", declared: "video/mp4", body: fakeMP4WithRawPayload([]byte("arbitrary payload"))},
		{name: "ebml track plus arbitrary block", expected: "video", declared: "video/webm", body: fakeEBMLVideoWithFrame([]byte("arbitrary payload"))},
		{name: "ogg page without codec headers", expected: "audio", declared: "audio/ogg", body: ogg},
		{name: "transport packets without tables or pes", expected: "video", declared: "video/mp2t", body: ts},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			prefix := test.body
			if len(prefix) > 512 {
				prefix = prefix[:512]
			}
			if err := validateMediaFile(test.expected, test.declared, prefix, bytes.NewReader(test.body), int64(len(test.body))); err == nil {
				t.Fatal("synthetic container shape was accepted")
			}
		})
	}
}

func TestValidateMediaFileRejectsFabricatedJPEGPayload(t *testing.T) {
	fabricated := []byte{0xff, 0xd8, 0xff, 0xe0, 0x00, 0x10, 'J', 'F', 'I', 'F', 0, 1, 0, 0, 0, 1, 0, 1, 0, 0, 0xff, 0xd9}
	if err := validateMediaFile("photo", "image/jpeg", fabricated, bytes.NewReader(fabricated), int64(len(fabricated))); err == nil {
		t.Fatal("fabricated JPEG payload was accepted")
	}
}

func TestValidateMediaFileRejectsFabricatedMP4Payload(t *testing.T) {
	fabricated := fakeMP4WithPayload([]byte{0})
	if err := validateMediaFile("video", "video/mp4", fabricated, bytes.NewReader(fabricated), int64(len(fabricated))); err == nil {
		t.Fatal("fabricated MP4 payload was accepted")
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
		{sourceType: "mastodon_bookmark", want: "mastodon"},
		{sourceType: "mastodon_quote", want: "mastodon"},
		{sourceType: "mastodon_reblog", want: "mastodon"},
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
		_, _ = w.Write(fakeJPEGBytes())
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
	if stats.Blocked != 1 || stats.Errors != 0 || stats.Downloaded != 0 {
		t.Fatalf("unexpected playlist stats: %+v", stats)
	}
	refs, err := st.ListItemMediaRefs(ctx, itemID)
	if err != nil {
		t.Fatalf("ListItemMediaRefs: %v", err)
	}
	if len(refs) != 1 || refs[0].DownloadStatus != model.MediaDownloadStatusBlocked || refs[0].LocalPath != "" {
		t.Fatalf("playlist ref = %#v", refs)
	}
	assertNoMediaFiles(t, cfg.MediaDir)
}

func TestRunForItemBlocksDeterministicMediaFailuresOnFirstAttempt(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name      string
		mediaType string
		mimeType  string
		body      []byte
		status    int
		maxBytes  int64
	}{
		{name: "html", mediaType: "video", mimeType: "text/html", body: []byte("<html>error</html>"), status: http.StatusOK},
		{name: "playlist", mediaType: "video", mimeType: "application/vnd.apple.mpegurl", body: []byte("#EXTM3U\n"), status: http.StatusOK},
		{name: "oversize", mediaType: "photo", mimeType: "image/jpeg", body: fakeJPEGBytes(), status: http.StatusOK, maxBytes: 4},
		{name: "unsupported", mediaType: "video", mimeType: "application/pdf", body: []byte("%PDF-1.7\n"), status: http.StatusOK},
		{name: "invalid-container", mediaType: "audio", mimeType: "audio/ogg", body: []byte("OggS"), status: http.StatusOK},
		{name: "unavailable", mediaType: "photo", mimeType: "text/plain", body: []byte("forbidden"), status: http.StatusForbidden},
	}

	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			hits := 0
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				hits++
				w.Header().Set("content-type", test.mimeType)
				w.WriteHeader(test.status)
				_, _ = w.Write(test.body)
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
			itemID := insertTestItem(t, st, "x:terminal-media-"+test.name, time.Now().UTC())
			if _, err := st.SaveItemMediaCandidates(context.Background(), itemID, []model.MediaCandidate{{RemoteURL: server.URL + "/asset", MediaType: test.mediaType}}); err != nil {
				t.Fatalf("SaveItemMediaCandidates: %v", err)
			}
			opts := Options{HTTPPolicy: privateNetworkTestPolicy()}
			if test.maxBytes > 0 {
				opts.MaxBytes = test.maxBytes
			}
			stats, err := RunForItem(context.Background(), cfg, st, itemID, opts)
			if err != nil {
				t.Fatalf("RunForItem: %v", err)
			}
			if stats.Blocked != 1 || stats.Errors != 0 || stats.Downloaded != 0 {
				t.Fatalf("first-attempt terminal stats = %+v", stats)
			}
			refs, err := st.ListItemMediaRefs(context.Background(), itemID)
			if err != nil || len(refs) != 1 || refs[0].DownloadStatus != model.MediaDownloadStatusBlocked {
				t.Fatalf("terminal media ref = %#v, err=%v", refs, err)
			}
			asset, err := st.GetMediaAsset(context.Background(), refs[0].MediaAssetID)
			if err != nil {
				t.Fatalf("GetMediaAsset: %v", err)
			}
			if strings.Contains(asset.DownloadError, "after 3 failed") {
				t.Fatalf("first-attempt terminal error falsely reports retries: %q", asset.DownloadError)
			}
			second, err := RunForItem(context.Background(), cfg, st, itemID, opts)
			if err != nil {
				t.Fatalf("second RunForItem: %v", err)
			}
			if second.Requested != 0 || hits != 1 {
				t.Fatalf("terminal media retried: second=%+v hits=%d", second, hits)
			}
		})
	}
}

func TestRunForItemUsesBlueskyMediaNamespace(t *testing.T) {
	t.Parallel()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("content-type", "image/jpeg")
		_, _ = w.Write(fakeJPEGBytes())
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
		_, _ = w.Write(genuineMP4VideoBytes())
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
		_, _ = w.Write(append(fakeJPEGBytes(), []byte(r.URL.Path)...))
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

	payload := largeGenuineMP4VideoBytes()
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

func fakeJPEGBytes() []byte {
	data, err := os.ReadFile("testdata/mastodon-image-jfif.jpg")
	if err != nil {
		panic(err)
	}
	return data
}

func largeGenuineMP4VideoBytes() []byte {
	result := append([]byte(nil), genuineMP4VideoBytes()...)
	reader := bytes.NewReader(result)
	var mdatOffset int64 = -1
	var mdatEnd int64
	for offset := int64(0); offset < int64(len(result)); {
		boxType, _, _, next, ok := readISOBoxHeader(reader, int64(len(result)), offset)
		if !ok {
			break
		}
		if boxType == "mdat" {
			mdatOffset = offset
			mdatEnd = next
			break
		}
		offset = next
	}
	if mdatOffset < 0 {
		panic("genuine MP4 fixture has no mdat box")
	}
	boxSize := binary.BigEndian.Uint32(result[mdatOffset : mdatOffset+4])
	if mdatEnd > int64(len(result)) {
		panic("genuine MP4 fixture has a truncated mdat box")
	}
	extra := bytes.Repeat([]byte("p"), 64*1024)
	result = append(result[:mdatEnd], append(extra, result[mdatEnd:]...)...)
	binary.BigEndian.PutUint32(result[mdatOffset:mdatOffset+4], boxSize+uint32(len(extra)))
	return result
}

func withGarbage(prefix []byte) []byte {
	result := append([]byte(nil), prefix...)
	return append(result, []byte("garbage")...)
}

func fakeMP4WithPayload(payload []byte) []byte {
	mediaPayload := append([]byte{0, 0, 0, 4, 0x65, 0x88, 0x00, 0x01}, payload...)
	return fakeMP4WithRawPayload(mediaPayload)
}

func fakeMP4WithRawPayload(mediaPayload []byte) []byte {
	result := []byte{0, 0, 0, 24, 'f', 't', 'y', 'p', 'i', 's', 'o', 'm', 0, 0, 0, 0, 'i', 's', 'o', 'm', 'i', 's', 'o', '2'}
	sampleEntry := make([]byte, 8+78+12)
	binary.BigEndian.PutUint32(sampleEntry[:4], uint32(len(sampleEntry)))
	copy(sampleEntry[4:8], []byte("avc1"))
	// A visual sample entry needs a codec configuration box before its media
	// payload can be treated as a valid MP4 rather than a superficial ftyp/stsd
	// shape. The remaining description bytes are intentionally zeroed because
	// these tests exercise downloader validation, not video playback.
	binary.BigEndian.PutUint32(sampleEntry[8+78:8+78+4], 12)
	copy(sampleEntry[8+78+4:8+78+8], []byte("avcC"))
	copy(sampleEntry[8+78+8:], []byte{1, 0x64, 0, 0x1f})
	stsdPayload := []byte{0, 0, 0, 0, 0, 0, 0, 1}
	stsdPayload = append(stsdPayload, sampleEntry...)
	stsdSize := 8 + len(stsdPayload)
	moovPayload := append([]byte{byte(stsdSize >> 24), byte(stsdSize >> 16), byte(stsdSize >> 8), byte(stsdSize), 's', 't', 's', 'd'}, stsdPayload...)
	moovSize := 8 + len(moovPayload)
	result = append(result, byte(moovSize>>24), byte(moovSize>>16), byte(moovSize>>8), byte(moovSize), 'm', 'o', 'o', 'v')
	result = append(result, moovPayload...)
	boxSize := 8 + len(mediaPayload)
	result = append(result, byte(boxSize>>24), byte(boxSize>>16), byte(boxSize>>8), byte(boxSize), 'm', 'd', 'a', 't')
	return append(result, mediaPayload...)
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
