package githubimport

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
)

func (c *client) viewer(ctx context.Context) (viewer, error) {
	var out viewer
	if err := c.getJSON(ctx, "/user", "application/vnd.github+json", &out); err != nil {
		return viewer{}, fmt.Errorf("load github viewer: %w", err)
	}
	if strings.TrimSpace(out.Login) == "" {
		return viewer{}, fmt.Errorf("github viewer login missing")
	}
	return out, nil
}

func (c *client) starredRepos(ctx context.Context, page int) ([]starRecord, error) {
	query := url.Values{}
	query.Set("sort", "created")
	query.Set("direction", "desc")
	query.Set("per_page", "100")
	query.Set("page", fmt.Sprintf("%d", page))
	var out []starRecord
	if err := c.getJSON(ctx, "/user/starred?"+query.Encode(), "application/vnd.github.star+json", &out); err != nil {
		return nil, fmt.Errorf("load github starred repos page %d: %w", page, err)
	}
	return out, nil
}

func (c *client) repoReadme(ctx context.Context, fullName string) (readmePayload, bool, error) {
	parts := strings.SplitN(fullName, "/", 2)
	if len(parts) != 2 {
		return readmePayload{}, false, fmt.Errorf("invalid repo full name %q", fullName)
	}
	req, err := c.request(ctx, http.MethodGet, "/repos/"+parts[0]+"/"+parts[1]+"/readme", "application/vnd.github+json")
	if err != nil {
		return readmePayload{}, false, err
	}
	resp, err := c.httpClient.Do(req)
	if err != nil {
		return readmePayload{}, false, err
	}
	defer func() {
		_ = resp.Body.Close()
	}()
	if resp.StatusCode == http.StatusNotFound {
		return readmePayload{}, false, nil
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		return readmePayload{}, false, fmt.Errorf("github readme request failed: %s: %s", resp.Status, strings.TrimSpace(string(body)))
	}
	var out readmePayload
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return readmePayload{}, false, fmt.Errorf("decode github readme: %w", err)
	}
	return out, true, nil
}

func (c *client) getJSON(ctx context.Context, path string, accept string, target any) error {
	req, err := c.request(ctx, http.MethodGet, path, accept)
	if err != nil {
		return err
	}
	resp, err := c.httpClient.Do(req)
	if err != nil {
		return err
	}
	defer func() {
		_ = resp.Body.Close()
	}()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		return fmt.Errorf("github request failed: %s: %s", resp.Status, strings.TrimSpace(string(body)))
	}
	if err := json.NewDecoder(resp.Body).Decode(target); err != nil {
		return fmt.Errorf("decode github response: %w", err)
	}
	return nil
}

func (c *client) request(ctx context.Context, method string, path string, accept string) (*http.Request, error) {
	req, err := http.NewRequestWithContext(ctx, method, c.baseURL+path, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+c.token)
	req.Header.Set("Accept", accept)
	req.Header.Set("X-GitHub-Api-Version", apiVersion)
	req.Header.Set("User-Agent", c.userAgent)
	return req, nil
}
