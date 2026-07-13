package sourceenrich

import (
	"context"
	"fmt"
	"io"
	"net/http"

	"github.com/darron/dbrain/internal/safehttp"
)

func newPublicHTTPClient(options ...Options) *http.Client {
	policy := httpPolicyForOptions(options)
	policy.AllowedPrivateOrigins = nil
	return safehttp.NewClient(policy)
}

func newConfiguredOriginHTTPClient(rawOrigin string, options ...Options) *http.Client {
	policy := httpPolicyForOptions(options)
	policy.AllowedPrivateOrigins = []string{rawOrigin}
	return safehttp.NewClient(policy)
}

func httpPolicyForOptions(options []Options) safehttp.Policy {
	if len(options) == 0 || options[0].httpPolicy == nil {
		if len(options) > 0 {
			return safehttp.Policy{Timeout: options[0].Timeout}
		}
		return safehttp.Policy{}
	}
	policy := *options[0].httpPolicy
	if policy.Timeout <= 0 {
		policy.Timeout = options[0].Timeout
	}
	return policy
}

func fetchHTTPText(ctx context.Context, client *http.Client, rawURL string) (*http.Response, string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
	if err != nil {
		return nil, "", fmt.Errorf("create request: %w", err)
	}
	req.Header.Set("user-agent", protectedFetchUserAgent)
	req.Header.Set("accept", "text/markdown, text/plain;q=0.95, text/html;q=0.9, application/xhtml+xml;q=0.8, application/xml;q=0.7, */*;q=0.5")
	req.Header.Set("accept-language", "en-US,en;q=0.9")

	resp, err := client.Do(req)
	if err != nil {
		return nil, "", fmt.Errorf("perform request: %w", err)
	}
	defer func() {
		_ = resp.Body.Close()
	}()

	body, err := io.ReadAll(io.LimitReader(resp.Body, maxProtectedBodyBytes))
	if err != nil {
		return nil, "", fmt.Errorf("read response body: %w", err)
	}
	return resp, string(body), nil
}

func fetchHTTPReaderText(ctx context.Context, client *http.Client, rawURL string) (*http.Response, string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
	if err != nil {
		return nil, "", fmt.Errorf("create request: %w", err)
	}
	req.Header.Set("user-agent", httpReaderUserAgent)
	req.Header.Set("accept", "text/plain,text/markdown,*/*;q=0.8")
	req.Header.Set("accept-language", "en-US,en;q=0.9")

	resp, err := client.Do(req)
	if err != nil {
		return nil, "", fmt.Errorf("perform request: %w", err)
	}
	defer func() {
		_ = resp.Body.Close()
	}()

	body, err := io.ReadAll(io.LimitReader(resp.Body, maxProtectedBodyBytes))
	if err != nil {
		return nil, "", fmt.Errorf("read response body: %w", err)
	}
	return resp, string(body), nil
}

func fetchWaybackText(ctx context.Context, client *http.Client, rawURL string, accept string) (*http.Response, string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
	if err != nil {
		return nil, "", fmt.Errorf("create request: %w", err)
	}
	req.Header.Set("user-agent", httpReaderUserAgent)
	req.Header.Set("accept", accept)
	req.Header.Set("accept-language", "en-US,en;q=0.9")

	resp, err := client.Do(req)
	if err != nil {
		return nil, "", fmt.Errorf("perform request: %w", err)
	}
	defer func() {
		_ = resp.Body.Close()
	}()

	body, err := io.ReadAll(io.LimitReader(resp.Body, maxProtectedBodyBytes))
	if err != nil {
		return nil, "", fmt.Errorf("read response body: %w", err)
	}
	return resp, string(body), nil
}
