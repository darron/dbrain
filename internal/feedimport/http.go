package feedimport

import (
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/darron/dbrain/internal/safehttp"
	"github.com/darron/dbrain/internal/store"
)

type HTTPFetcher struct {
	client *http.Client
}

type HTTPFetcherOptions struct {
	AllowPrivateNetwork bool
}

func NewHTTPFetcher(client *http.Client) HTTPFetcher {
	return NewHTTPFetcherWithOptions(client, HTTPFetcherOptions{})
}

func NewHTTPFetcherWithOptions(client *http.Client, opts HTTPFetcherOptions) HTTPFetcher {
	if client != nil {
		return HTTPFetcher{client: client}
	}
	return HTTPFetcher{client: safehttp.NewClient(safehttp.Policy{
		Timeout:             DefaultTimeout,
		MaxRedirects:        10,
		AllowPrivateNetwork: opts.AllowPrivateNetwork,
		DisableCompression:  true,
	})}
}

func (f HTTPFetcher) Fetch(ctx context.Context, feed store.Feed, opts Options) (FetchResult, error) {
	target := firstNonEmpty(feed.ResolvedURL, feed.NormalizedURL, feed.URL)
	if target == "" {
		return FetchResult{}, fmt.Errorf("feed has no URL")
	}
	parsedTarget, err := url.Parse(target)
	if err != nil {
		return FetchResult{}, fmt.Errorf("create feed request: %w", sanitizeFeedURLError(err))
	}
	credentials := parsedTarget.User
	parsedTarget.User = nil
	sanitizedTarget := parsedTarget.String()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, sanitizedTarget, nil)
	if err != nil {
		return FetchResult{}, fmt.Errorf("create feed request: %w", sanitizeFeedURLError(err))
	}
	if credentials != nil {
		password, _ := credentials.Password()
		req.SetBasicAuth(credentials.Username(), password)
	}
	req.Header.Set("user-agent", opts.UserAgent)
	req.Header.Set("accept", "application/feed+json, application/atom+xml, application/rss+xml, application/xml, text/xml, */*;q=0.1")
	req.Header.Set("accept-language", "en-US,en;q=0.9")
	if !opts.Force && feed.FetchETag != "" {
		req.Header.Set("if-none-match", feed.FetchETag)
	}
	if !opts.Force && feed.FetchLastModified != "" {
		req.Header.Set("if-modified-since", feed.FetchLastModified)
	}

	client := feedRequestClient(f.client, opts.Timeout, req.URL, credentials != nil)
	resp, err := client.Do(req)
	if err != nil {
		return FetchResult{}, fmt.Errorf("fetch feed: %w", sanitizeFeedURLError(err))
	}
	defer func() { _ = resp.Body.Close() }()

	finalURL := *resp.Request.URL
	finalURL.User = nil
	headersJSON, _ := json.Marshal(resp.Header)
	result := FetchResult{
		RequestURL:      sanitizedTarget,
		FinalURL:        finalURL.String(),
		HTTPStatus:      resp.StatusCode,
		HeadersJSON:     string(headersJSON),
		ContentEncoding: strings.TrimSpace(resp.Header.Get("content-encoding")),
		ETag:            strings.TrimSpace(resp.Header.Get("etag")),
		LastModified:    strings.TrimSpace(resp.Header.Get("last-modified")),
		RetryAfter:      parseRetryAfter(resp.Header.Get("retry-after"), time.Now().UTC()),
		NotModified:     resp.StatusCode == http.StatusNotModified,
	}
	if result.NotModified {
		result.DecodedBodyHash = feed.FetchBodyHash
		return result, nil
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		return result, fmt.Errorf("feed HTTP %d: %s", resp.StatusCode, strings.TrimSpace(string(body)))
	}

	wire, err := io.ReadAll(io.LimitReader(resp.Body, opts.MaxBodyBytes+1))
	if err != nil {
		return result, fmt.Errorf("read feed response: %w", err)
	}
	if int64(len(wire)) > opts.MaxBodyBytes {
		return result, fmt.Errorf("feed response exceeds %d decoded bytes", opts.MaxBodyBytes)
	}
	result.WireResponseBytes = wire
	decoded := wire
	if strings.EqualFold(result.ContentEncoding, "gzip") {
		reader, err := gzip.NewReader(bytes.NewReader(wire))
		if err != nil {
			return result, fmt.Errorf("decode gzip feed response: %w", err)
		}
		defer func() { _ = reader.Close() }()
		decoded, err = io.ReadAll(io.LimitReader(reader, opts.MaxBodyBytes+1))
		if err != nil {
			return result, fmt.Errorf("read gzip feed response: %w", err)
		}
		if int64(len(decoded)) > opts.MaxBodyBytes {
			return result, fmt.Errorf("feed response exceeds %d decoded bytes", opts.MaxBodyBytes)
		}
	}
	result.DecodedBody = decoded
	result.DecodedSizeBytes = int64(len(decoded))
	result.DecodedBodyHash = sha256Hex(decoded)
	if !opts.Force && feed.FetchBodyHash != "" && result.DecodedBodyHash == feed.FetchBodyHash {
		result.UnchangedBody = true
		result.WireResponseBytes = nil
		result.DecodedBody = nil
	}
	return result, nil
}

func feedRequestClient(client *http.Client, timeout time.Duration, credentialOrigin *url.URL, hasCredentials bool) *http.Client {
	clone := *client
	if clone.Timeout <= 0 {
		clone.Timeout = timeout
	}
	if !hasCredentials {
		return &clone
	}

	originalCheckRedirect := clone.CheckRedirect
	origin := *credentialOrigin
	clone.CheckRedirect = func(req *http.Request, via []*http.Request) error {
		if !sameFeedHTTPOrigin(&origin, req.URL) {
			req.Header.Del("Authorization")
		}
		if originalCheckRedirect != nil {
			return originalCheckRedirect(req, via)
		}
		if len(via) >= 10 {
			return errors.New("stopped after 10 redirects")
		}
		return nil
	}
	return &clone
}

func sameFeedHTTPOrigin(left *url.URL, right *url.URL) bool {
	leftOrigin, leftOK := normalizedFeedHTTPOrigin(left)
	rightOrigin, rightOK := normalizedFeedHTTPOrigin(right)
	return leftOK && rightOK && leftOrigin == rightOrigin
}

func normalizedFeedHTTPOrigin(target *url.URL) (string, bool) {
	if target == nil {
		return "", false
	}
	scheme := strings.ToLower(strings.TrimSpace(target.Scheme))
	host := strings.ToLower(strings.TrimSuffix(strings.TrimSpace(target.Hostname()), "."))
	if (scheme != "http" && scheme != "https") || host == "" {
		return "", false
	}
	port := target.Port()
	if port == "" {
		if scheme == "https" {
			port = "443"
		} else {
			port = "80"
		}
	}
	return scheme + "://" + net.JoinHostPort(host, port), true
}

func sanitizeFeedURLError(err error) error {
	var urlErr *url.Error
	if !errors.As(err, &urlErr) {
		return err
	}
	clone := *urlErr
	clone.URL = sanitizeFeedURLUserInfo(urlErr.URL)
	clone.Err = sanitizeFeedNestedError(urlErr.Err)
	return &clone
}

type sanitizedFeedError struct {
	message string
	cause   error
}

func (e *sanitizedFeedError) Error() string {
	return e.message
}

func (e *sanitizedFeedError) Unwrap() error {
	return e.cause
}

func sanitizeFeedNestedError(err error) error {
	if err == nil {
		return nil
	}
	message := sanitizeFeedCredentialURLsInText(err.Error())
	if message == err.Error() {
		return err
	}
	return &sanitizedFeedError{message: message, cause: err}
}

func sanitizeFeedCredentialURLsInText(value string) string {
	searchFrom := 0
	for searchFrom < len(value) {
		urlStart, authorityStart, ok := nextFeedURLAuthority(value, searchFrom)
		if !ok {
			break
		}
		authorityEnd := len(value)
		if offset := strings.IndexAny(value[authorityStart:], "/?# \t\r\n\"'<>"); offset >= 0 {
			authorityEnd = authorityStart + offset
		}
		userinfoEnd := strings.LastIndex(value[authorityStart:authorityEnd], "@")
		if userinfoEnd < 0 {
			searchFrom = authorityEnd
			if searchFrom <= urlStart {
				searchFrom = urlStart + 1
			}
			continue
		}
		userinfoEnd += authorityStart
		value = value[:authorityStart] + value[userinfoEnd+1:]
		searchFrom = authorityStart
	}
	return value
}

func nextFeedURLAuthority(value string, searchFrom int) (int, int, bool) {
	lower := strings.ToLower(value[searchFrom:])
	bestOffset := -1
	prefixLength := 0
	for _, prefix := range []string{"http://", "https://", "//"} {
		offset := strings.Index(lower, prefix)
		if offset >= 0 && (bestOffset < 0 || offset < bestOffset) {
			bestOffset = offset
			prefixLength = len(prefix)
		}
	}
	if bestOffset < 0 {
		return 0, 0, false
	}
	urlStart := searchFrom + bestOffset
	return urlStart, urlStart + prefixLength, true
}

func sanitizeFeedURLUserInfo(raw string) string {
	parsed, err := url.Parse(raw)
	if err == nil {
		parsed.User = nil
		return parsed.String()
	}
	return sanitizeFeedCredentialURLsInText(raw)
}

func sha256Hex(body []byte) string {
	sum := sha256.Sum256(body)
	return hex.EncodeToString(sum[:])
}

func parseRetryAfter(value string, now time.Time) time.Time {
	value = strings.TrimSpace(value)
	if value == "" {
		return time.Time{}
	}
	if seconds, err := strconv.Atoi(value); err == nil && seconds > 0 {
		return now.Add(time.Duration(seconds) * time.Second).UTC()
	}
	if parsed, err := http.ParseTime(value); err == nil {
		return parsed.UTC()
	}
	return time.Time{}
}
