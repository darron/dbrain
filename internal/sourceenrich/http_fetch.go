package sourceenrich

import (
	"context"
	"fmt"
	"io"
	"net/http"
)

func fetchHTTPText(ctx context.Context, client *http.Client, rawURL string) (*http.Response, string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
	if err != nil {
		return nil, "", fmt.Errorf("create request: %w", err)
	}
	req.Header.Set("user-agent", protectedFetchUserAgent)
	req.Header.Set("accept", "text/html,application/xhtml+xml,application/xml;q=0.9,*/*;q=0.8")
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
