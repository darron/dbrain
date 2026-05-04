package sourceenrich

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	neturl "net/url"
	"strings"

	"github.com/darron/dbrain/internal/model"
)

type waybackAvailabilityEnvelope struct {
	ArchivedSnapshots struct {
		Closest struct {
			Available bool   `json:"available"`
			URL       string `json:"url"`
			Timestamp string `json:"timestamp"`
			Status    string `json:"status"`
		} `json:"closest"`
	} `json:"archived_snapshots"`
}

func waybackExtractForTerminalFailure(ctx context.Context, source model.SourceDocument, opts Options, fetchErr error) (model.ExtractResult, bool, error) {
	shouldTry, kind, nextCount, threshold := shouldTryWaybackForSourceError(source, fetchErr)
	if !opts.WaybackFallbackEnabled || !shouldTry {
		return model.ExtractResult{}, false, nil
	}
	sourceURL := firstNonEmpty(source.CanonicalURL, source.NormalizedURL)
	if strings.TrimSpace(sourceURL) == "" {
		return model.ExtractResult{}, false, nil
	}
	if _, ok := sourceHost(sourceURL); !ok {
		return model.ExtractResult{}, false, nil
	}
	debugLog(opts.Logger, "source wayback fallback checking", "source_key", source.SourceKey, "url", sourceURL, "failure_kind", kind, "next_failure_count", nextCount, "threshold", threshold)
	extract, recovered, err := extractWaybackArchivedSource(ctx, sourceURL, opts)
	if err != nil {
		return extract, recovered, err
	}
	if !recovered {
		debugLog(opts.Logger, "source wayback fallback found no usable snapshot", "source_key", source.SourceKey, "url", sourceURL, "failure_kind", kind, "next_failure_count", nextCount)
	}
	return extract, recovered, nil
}

func shouldTryWaybackForSourceError(source model.SourceDocument, fetchErr error) (bool, string, int, int) {
	if fetchErr == nil {
		return false, "", 0, 0
	}
	kind := classifyExtractFailureKind(fetchErr.Error())
	if kind == "" {
		kind = "unknown"
	}
	threshold := deadThresholdForFailureKind(kind)
	if threshold <= 0 {
		return false, kind, 0, threshold
	}
	nextCount := nextFailureCount(source, kind)
	return nextCount >= threshold, kind, nextCount, threshold
}

func extractWaybackArchivedSource(ctx context.Context, sourceURL string, opts Options) (model.ExtractResult, bool, error) {
	availabilityURL := buildWaybackAvailabilityURL(opts.WaybackAvailabilityURL, sourceURL)
	if availabilityURL == "" {
		return model.ExtractResult{}, false, nil
	}

	client := &http.Client{}
	resp, body, err := fetchWaybackText(ctx, client, availabilityURL, "application/json,*/*;q=0.8")
	if err != nil {
		return model.ExtractResult{}, false, fmt.Errorf("fetch wayback availability: %w", err)
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return model.ExtractResult{}, false, fmt.Errorf("fetch wayback availability: unexpected status %d", resp.StatusCode)
	}

	var payload waybackAvailabilityEnvelope
	if err := json.Unmarshal([]byte(body), &payload); err != nil {
		return model.ExtractResult{}, false, fmt.Errorf("decode wayback availability: %w", err)
	}
	closest := payload.ArchivedSnapshots.Closest
	if !closest.Available || strings.TrimSpace(closest.URL) == "" {
		return model.ExtractResult{}, false, nil
	}
	if closest.Status != "" && !strings.HasPrefix(closest.Status, "2") {
		return model.ExtractResult{}, false, nil
	}

	rawURL := waybackRawSnapshotURL(closest.URL, closest.Timestamp, sourceURL)
	if rawURL == "" {
		return model.ExtractResult{}, false, nil
	}

	snapshotResp, snapshotBody, err := fetchWaybackText(ctx, client, rawURL, "text/html,application/xhtml+xml,application/xml;q=0.9,*/*;q=0.8")
	if err != nil {
		return model.ExtractResult{}, false, fmt.Errorf("fetch wayback snapshot: %w", err)
	}
	if snapshotResp.StatusCode < 200 || snapshotResp.StatusCode >= 300 {
		return model.ExtractResult{}, false, fmt.Errorf("fetch wayback snapshot: unexpected status %d", snapshotResp.StatusCode)
	}

	extract := extractHTMLSource(sourceURL, snapshotResp, snapshotBody, "wayback-html", waybackToolVersion, closest.Timestamp)
	extract.Tool = waybackToolName
	extract.ToolVersion = waybackToolVersion
	extract.RawJSON = buildProtectedRawJSON("wayback-html", sourceURL, rawURL, extract.Title, extract.Description, extract.SiteName, extract.Content, closest.Timestamp)
	return extract, strings.TrimSpace(extract.Content) != "", nil
}

func buildWaybackAvailabilityURL(baseURL string, sourceURL string) string {
	baseURL = firstNonEmpty(strings.TrimSpace(baseURL), defaultWaybackAvailabilityURL)
	sourceURL = strings.TrimSpace(sourceURL)
	if baseURL == "" || sourceURL == "" {
		return ""
	}
	if strings.Contains(baseURL, "{escaped_url}") {
		return strings.ReplaceAll(baseURL, "{escaped_url}", neturl.QueryEscape(sourceURL))
	}
	if strings.Contains(baseURL, "{url}") {
		return strings.ReplaceAll(baseURL, "{url}", sourceURL)
	}
	separator := "?"
	if strings.Contains(baseURL, "?") {
		separator = "&"
	}
	return baseURL + separator + "url=" + neturl.QueryEscape(sourceURL)
}

func waybackRawSnapshotURL(snapshotURL string, timestamp string, sourceURL string) string {
	snapshotURL = strings.TrimSpace(snapshotURL)
	timestamp = strings.TrimSpace(timestamp)
	sourceURL = strings.TrimSpace(sourceURL)
	if snapshotURL == "" || sourceURL == "" {
		return ""
	}

	parsed, err := neturl.Parse(snapshotURL)
	if err != nil || parsed.Scheme == "" || parsed.Host == "" {
		return ""
	}
	if timestamp == "" {
		parts := strings.Split(strings.Trim(parsed.Path, "/"), "/")
		if len(parts) >= 2 && parts[0] == "web" {
			timestamp = strings.TrimSuffix(parts[1], "id_")
		}
	}
	if timestamp == "" {
		return ""
	}
	return parsed.Scheme + "://" + parsed.Host + "/web/" + timestamp + "id_/" + sourceURL
}
