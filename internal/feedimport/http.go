package feedimport

import (
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/darron/dbrain/internal/store"
)

type HTTPFetcher struct {
	client *http.Client
}

func NewHTTPFetcher(client *http.Client) HTTPFetcher {
	if client != nil {
		return HTTPFetcher{client: client}
	}
	return HTTPFetcher{client: &http.Client{
		Timeout: DefaultTimeout,
		Transport: &http.Transport{
			Proxy:              http.ProxyFromEnvironment,
			DisableCompression: true,
			DialContext:        safeDialContext,
		},
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			if len(via) >= 10 {
				return fmt.Errorf("too many redirects")
			}
			return nil
		},
	}}
}

func (f HTTPFetcher) Fetch(ctx context.Context, feed store.Feed, opts Options) (FetchResult, error) {
	target := firstNonEmpty(feed.ResolvedURL, feed.NormalizedURL, feed.URL)
	if target == "" {
		return FetchResult{}, fmt.Errorf("feed has no URL")
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, target, nil)
	if err != nil {
		return FetchResult{}, fmt.Errorf("create feed request: %w", err)
	}
	req.Header.Set("user-agent", opts.UserAgent)
	req.Header.Set("accept", "application/feed+json, application/atom+xml, application/rss+xml, application/xml, text/xml, */*;q=0.1")
	req.Header.Set("accept-language", "en-US,en;q=0.9")
	if feed.FetchETag != "" {
		req.Header.Set("if-none-match", feed.FetchETag)
	}
	if feed.FetchLastModified != "" {
		req.Header.Set("if-modified-since", feed.FetchLastModified)
	}

	client := f.client
	if client.Timeout <= 0 {
		clone := *client
		clone.Timeout = opts.Timeout
		client = &clone
	}
	resp, err := client.Do(req)
	if err != nil {
		return FetchResult{}, fmt.Errorf("fetch feed: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	headersJSON, _ := json.Marshal(resp.Header)
	result := FetchResult{
		RequestURL:      target,
		FinalURL:        resp.Request.URL.String(),
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

func safeDialContext(ctx context.Context, network, address string) (net.Conn, error) {
	host, port, err := net.SplitHostPort(address)
	if err != nil {
		return nil, err
	}
	ips, err := net.DefaultResolver.LookupIPAddr(ctx, host)
	if err != nil {
		return nil, err
	}
	for _, candidate := range ips {
		ip := candidate.IP
		if !isPublicIP(ip) {
			continue
		}
		var dialer net.Dialer
		return dialer.DialContext(ctx, network, net.JoinHostPort(ip.String(), port))
	}
	return nil, fmt.Errorf("no public IPs resolved for %s", host)
}

func isPublicIP(ip net.IP) bool {
	if ip == nil {
		return false
	}
	return !ip.IsUnspecified() &&
		!ip.IsLoopback() &&
		!ip.IsPrivate() &&
		!ip.IsLinkLocalUnicast() &&
		!ip.IsLinkLocalMulticast() &&
		!ip.IsMulticast()
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
