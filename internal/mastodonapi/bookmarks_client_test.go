package mastodonapi

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"
)

func TestFetchBookmarksPageUsesBearerOnlyForAPIAndParsesNextCursor(t *testing.T) {
	body := `[ {"id":"1","uri":"https://hachyderm.io/users/alice/statuses/1","account":{"id":"42","username":"alice"},"content":"hello"} ]`
	client, err := NewClient("https://hachyderm.io", "secret-token", &http.Client{Transport: roundTripFunc(func(request *http.Request) (*http.Response, error) {
		if request.URL.Path != "/api/v1/bookmarks" || request.URL.Query().Get("limit") != "40" {
			t.Fatalf("unexpected request URL: %s", request.URL.String())
		}
		if got := request.Header.Get("Authorization"); got != "Bearer secret-token" {
			t.Fatalf("authorization = %q", got)
		}
		return &http.Response{StatusCode: http.StatusOK, Header: http.Header{"Link": []string{`<https://hachyderm.io/api/v1/bookmarks?limit=40&max_id=opaque>; rel="next"`}}, Body: io.NopCloser(strings.NewReader(body))}, nil
	})})
	if err != nil {
		t.Fatalf("NewClient: %v", err)
	}
	page, err := client.FetchBookmarksPage(context.Background(), "", 40)
	if err != nil {
		t.Fatalf("FetchBookmarksPage: %v", err)
	}
	if len(page.Statuses) != 1 || page.NextURL == "" || page.PageDigest == "" {
		t.Fatalf("page = %#v", page)
	}
	digest := sha256.Sum256([]byte(body))
	if page.PageDigest != hex.EncodeToString(digest[:]) {
		t.Fatalf("digest = %q", page.PageDigest)
	}
}

func TestFetchBookmarksPageClassifiesStaleOpaqueCursor(t *testing.T) {
	client, err := NewClient("https://hachyderm.io", "token", &http.Client{Transport: roundTripFunc(func(request *http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: http.StatusGone, Body: io.NopCloser(strings.NewReader("gone"))}, nil
	})})
	if err != nil {
		t.Fatalf("NewClient: %v", err)
	}
	_, err = client.FetchBookmarksPage(context.Background(), "https://hachyderm.io/api/v1/bookmarks?limit=40&max_id=opaque", 40)
	var stale *StaleCursorError
	if !errors.As(err, &stale) {
		t.Fatalf("error = %v, want StaleCursorError", err)
	}
}

func TestFetchBookmarksPageRejectsNullResponse(t *testing.T) {
	client, err := NewClient("https://hachyderm.io", "token", &http.Client{Transport: roundTripFunc(func(request *http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(strings.NewReader("null"))}, nil
	})})
	if err != nil {
		t.Fatalf("NewClient: %v", err)
	}
	if _, err := client.FetchBookmarksPage(context.Background(), "", 40); err == nil {
		t.Fatal("FetchBookmarksPage accepted JSON null as an empty bookmark page")
	}
}

func TestClientWithTimeoutClonesHTTPClient(t *testing.T) {
	original := &Client{Origin: "https://hachyderm.io:443", HTTPClient: &http.Client{Timeout: 30 * time.Second}}
	adjusted := clientWithTimeout(original, 90*time.Second)
	if adjusted == original || adjusted.HTTPClient == original.HTTPClient {
		t.Fatal("clientWithTimeout reused mutable client state")
	}
	if got := adjusted.HTTPClient.Timeout; got != 90*time.Second {
		t.Fatalf("adjusted timeout = %s, want 1m30s", got)
	}
	if got := original.HTTPClient.Timeout; got != 30*time.Second {
		t.Fatalf("original timeout changed to %s", got)
	}
}

func TestBoundedRetryAfterParsesSecondsAndHTTPDate(t *testing.T) {
	if got := boundedRetryAfter("7"); got != 7*time.Second {
		t.Fatalf("seconds retry-after = %s, want 7s", got)
	}
	if got := boundedRetryAfter("999999"); got != 5*time.Minute {
		t.Fatalf("capped retry-after = %s, want 5m", got)
	}
	if got := boundedRetryAfter(time.Now().Add(2 * time.Second).UTC().Format(http.TimeFormat)); got <= 0 || got > 2*time.Second {
		t.Fatalf("HTTP-date retry-after = %s, want a short positive delay", got)
	}
}

func TestFetchMastodonBookmarksPageReportsAPIAndRateLimitRetries(t *testing.T) {
	attempts := 0
	client, err := NewClient("https://hachyderm.io", "token", &http.Client{Transport: roundTripFunc(func(request *http.Request) (*http.Response, error) {
		attempts++
		if attempts == 1 {
			return &http.Response{StatusCode: http.StatusTooManyRequests, Header: http.Header{"Retry-After": []string{"1"}}, Body: io.NopCloser(strings.NewReader("busy"))}, nil
		}
		return &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(strings.NewReader("[]"))}, nil
	})})
	if err != nil {
		t.Fatalf("NewClient: %v", err)
	}
	page, stats, err := fetchMastodonBookmarksPageWithStats(context.Background(), client, "", 40, 2*time.Second)
	if err != nil {
		t.Fatalf("fetchMastodonBookmarksPageWithStats: %v", err)
	}
	if len(page.Statuses) != 0 || attempts != 2 || stats.APIErrors != 1 || stats.RateLimits != 1 || stats.Retries != 1 {
		t.Fatalf("page=%#v attempts=%d stats=%#v", page, attempts, stats)
	}
}
