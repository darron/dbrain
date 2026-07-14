package sourceenrich

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	neturl "net/url"
	"strings"
	"time"

	"github.com/darron/dbrain/internal/model"
	"github.com/darron/dbrain/internal/summarizecli"
)

func preflightTerminalSourceFailure(ctx context.Context, source model.SourceDocument, opts Options, toolVersion string) (model.ExtractResult, bool) {
	host, ok := sourceHost(source.CanonicalURL)
	if !ok {
		return model.ExtractResult{}, false
	}

	resolveHost := opts.ResolveHost
	if resolveHost == nil {
		resolveHost = defaultResolveHost
	}

	lookupCtx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	if err := resolveHost(lookupCtx, host); err != nil {
		if isHostNotFoundError(err) {
			return model.ExtractResult{
				Status:      model.SourceExtractStatusDead,
				Error:       fmt.Sprintf("host does not resolve: %s", host),
				Tool:        summarizecli.ToolName,
				ToolVersion: toolVersion,
			}, true
		}
	}

	return model.ExtractResult{}, false
}

func sourceHost(rawURL string) (string, bool) {
	if strings.TrimSpace(rawURL) == "" {
		return "", false
	}
	parsed, err := neturl.Parse(rawURL)
	if err != nil {
		return "", false
	}
	switch strings.ToLower(parsed.Scheme) {
	case "http", "https":
	default:
		return "", false
	}
	host := strings.TrimSpace(parsed.Hostname())
	if host == "" {
		return "", false
	}
	return host, true
}

func defaultResolveHost(ctx context.Context, host string) error {
	_, err := net.DefaultResolver.LookupHost(ctx, host)
	return err
}

func defaultResolveRedirectURL(ctx context.Context, rawURL string, options ...Options) (string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
	if err != nil {
		return "", fmt.Errorf("create redirect resolution request: %w", err)
	}
	req.Header.Set("user-agent", "dbrain/1.0")

	resp, err := newPublicHTTPClient(options...).Do(req)
	if err != nil {
		return "", fmt.Errorf("resolve redirect: %w", err)
	}
	defer func() {
		_, _ = io.CopyN(io.Discard, resp.Body, 1024)
		_ = resp.Body.Close()
	}()

	if resp.Request == nil || resp.Request.URL == nil {
		return rawURL, nil
	}
	return resp.Request.URL.String(), nil
}

func isHostNotFoundError(err error) bool {
	if err == nil {
		return false
	}
	var dnsErr *net.DNSError
	if errors.As(err, &dnsErr) && dnsErr.IsNotFound {
		return true
	}
	value := strings.ToLower(err.Error())
	return strings.Contains(value, "no such host") || strings.Contains(value, "nxdomain")
}

func isRedirectFetchError(err error) bool {
	if err == nil {
		return false
	}
	value := strings.ToLower(strings.TrimSpace(err.Error()))
	for _, status := range []string{"status 301", "status 302", "status 303", "status 307", "status 308"} {
		if strings.Contains(value, status) {
			return true
		}
	}
	return false
}
