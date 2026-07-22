package sourceenrich

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/darron/dbrain/internal/model"
)

const (
	protectedFetchToolName        = "http-fallback"
	protectedFetchToolVersion     = "sucuri-js-v1"
	httpReaderToolVersion         = "http-reader-v1"
	waybackToolName               = "wayback"
	waybackToolVersion            = "wayback-v1"
	defaultWaybackAvailabilityURL = "https://archive.org/wayback/available?url={escaped_url}"
	protectedFetchUserAgent       = "Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/135.0.0.0 Safari/537.36"
	httpReaderUserAgent           = "curl/8.7.1"
	maxProtectedBodyBytes         = 8 << 20
)

func fallbackExtractForSourceError(ctx context.Context, source model.SourceDocument, opts Options, fetchErr error) (model.ExtractResult, bool, error) {
	sourceURL := firstNonEmpty(source.CanonicalURL, source.NormalizedURL)
	if strings.TrimSpace(sourceURL) == "" {
		return model.ExtractResult{}, false, nil
	}

	timeout := 30 * time.Second
	if opts.Timeout > 0 && opts.Timeout < timeout {
		timeout = opts.Timeout
	}
	fetchCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	var recoveryErr error
	if isRedirectFetchError(fetchErr) {
		extract, recovered, err := extractProtectedSource(fetchCtx, sourceURL, opts)
		if recovered {
			return extract, recovered, err
		}
		if err != nil {
			recoveryErr = err
		}
	}
	if isHTTPReaderFallbackCandidate(source, opts, fetchErr) {
		extract, recovered, err := extractHTTPReadableSource(fetchCtx, sourceURL, opts)
		if recovered || err == nil {
			return extract, recovered, err
		}
		recoveryErr = err
	}
	waybackExtract, recovered, waybackErr := waybackExtractForTerminalFailure(fetchCtx, source, opts, fetchErr)
	if recovered || waybackErr != nil {
		if waybackErr != nil && recoveryErr != nil {
			return model.ExtractResult{}, false, fmt.Errorf("%v; wayback recovery failed: %w", recoveryErr, waybackErr)
		}
		return waybackExtract, recovered, waybackErr
	}
	if recoveryErr != nil {
		return model.ExtractResult{}, false, recoveryErr
	}
	return model.ExtractResult{}, false, nil
}
