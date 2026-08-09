package mastodonapi

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/darron/dbrain/internal/runtimeenv"
	"github.com/darron/dbrain/internal/safehttp"
)

const maxMastodonAPIResponseBytes int64 = 2 << 20

// DiscoverOAuthEndpoints learns optional OAuth metadata while retaining
// Mastodon's same-origin defaults when metadata is absent.
func DiscoverOAuthEndpoints(ctx context.Context, origin string, client *http.Client) (OAuthEndpoints, error) {
	canonical, err := canonicalOrigin(origin)
	if err != nil {
		return OAuthEndpoints{}, fmt.Errorf("validate Mastodon origin: %w", err)
	}
	if client == nil {
		client = safehttp.NewClient(safehttp.Policy{
			Timeout:        20 * time.Second,
			AllowedOrigins: []string{canonical},
		})
	}
	instance, err := fetchJSON(ctx, client, strings.TrimRight(canonical, "/")+"/api/v2/instance", maxMastodonAPIResponseBytes)
	if err != nil {
		return OAuthEndpoints{}, fmt.Errorf("discover Mastodon instance: %w", err)
	}
	if instance.StatusCode < 200 || instance.StatusCode >= 300 {
		return OAuthEndpoints{}, fmt.Errorf("discover Mastodon instance: HTTP %d", instance.StatusCode)
	}
	var instanceDoc struct {
		Version string `json:"version"`
	}
	if err := json.Unmarshal(instance.Body, &instanceDoc); err != nil {
		return OAuthEndpoints{}, fmt.Errorf("decode Mastodon instance metadata: %w", err)
	}

	endpoints := OAuthEndpoints{
		AuthorizationURL: strings.TrimRight(canonical, "/") + "/oauth/authorize",
		TokenURL:         strings.TrimRight(canonical, "/") + "/oauth/token",
		RevocationURL:    strings.TrimRight(canonical, "/") + "/oauth/revoke",
		Version:          strings.TrimSpace(instanceDoc.Version),
		SupportsPKCE:     mastodonVersionSupportsPKCE(instanceDoc.Version),
	}
	metadata, err := fetchJSON(ctx, client, strings.TrimRight(canonical, "/")+"/.well-known/oauth-authorization-server", maxMastodonAPIResponseBytes)
	if err != nil {
		return OAuthEndpoints{}, fmt.Errorf("discover Mastodon OAuth metadata: %w", err)
	}
	if metadata.StatusCode == http.StatusNotFound || metadata.StatusCode == http.StatusMethodNotAllowed {
		return endpoints, nil
	}
	if metadata.StatusCode < 200 || metadata.StatusCode >= 300 {
		return OAuthEndpoints{}, fmt.Errorf("discover Mastodon OAuth metadata: HTTP %d", metadata.StatusCode)
	}
	var doc struct {
		Issuer                string   `json:"issuer"`
		AuthorizationEndpoint string   `json:"authorization_endpoint"`
		TokenEndpoint         string   `json:"token_endpoint"`
		RevocationEndpoint    string   `json:"revocation_endpoint"`
		CodeChallengeMethods  []string `json:"code_challenge_methods_supported"`
	}
	if err := json.Unmarshal(metadata.Body, &doc); err != nil {
		return OAuthEndpoints{}, fmt.Errorf("decode Mastodon OAuth metadata: %w", err)
	}
	for name, value := range map[string]string{
		"issuer":                 doc.Issuer,
		"authorization endpoint": doc.AuthorizationEndpoint,
		"token endpoint":         doc.TokenEndpoint,
		"revocation endpoint":    doc.RevocationEndpoint,
	} {
		if strings.TrimSpace(value) == "" {
			continue
		}
		if _, err := sameOriginEndpoint(canonical, value); err != nil {
			return OAuthEndpoints{}, fmt.Errorf("validate OAuth %s: %w", name, err)
		}
	}
	if doc.AuthorizationEndpoint != "" {
		endpoints.AuthorizationURL = doc.AuthorizationEndpoint
	}
	if doc.TokenEndpoint != "" {
		endpoints.TokenURL = doc.TokenEndpoint
	}
	if doc.RevocationEndpoint != "" {
		endpoints.RevocationURL = doc.RevocationEndpoint
	}
	for _, method := range doc.CodeChallengeMethods {
		if strings.EqualFold(strings.TrimSpace(method), "S256") {
			endpoints.SupportsPKCE = true
			break
		}
	}
	return endpoints, nil
}

type boundedResponse struct {
	StatusCode int
	Body       []byte
}

func fetchJSON(ctx context.Context, client *http.Client, rawURL string, maxBytes int64) (boundedResponse, error) {
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
	if err != nil {
		return boundedResponse{}, err
	}
	response, err := client.Do(request)
	if err != nil {
		return boundedResponse{}, err
	}
	defer func() { _ = response.Body.Close() }()
	if response.ContentLength > maxBytes {
		return boundedResponse{}, fmt.Errorf("response exceeds %d bytes", maxBytes)
	}
	body, err := io.ReadAll(io.LimitReader(response.Body, maxBytes+1))
	if err != nil {
		return boundedResponse{}, err
	}
	if int64(len(body)) > maxBytes {
		return boundedResponse{}, fmt.Errorf("response exceeds %d bytes", maxBytes)
	}
	return boundedResponse{StatusCode: response.StatusCode, Body: body}, nil
}

func sameOriginEndpoint(origin, raw string) (string, error) {
	parsed, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || parsed.Scheme != "https" || parsed.Host == "" || parsed.User != nil || parsed.RawQuery != "" || parsed.Fragment != "" {
		return "", fmt.Errorf("endpoint must be an absolute HTTPS URL without credentials, query, or fragment")
	}
	endpointOrigin, err := safehttp.CanonicalOriginEndpoint(parsed.Scheme + "://" + parsed.Host)
	if err != nil {
		return "", err
	}
	if endpointOrigin != origin {
		return "", fmt.Errorf("endpoint origin %s differs from configured origin %s", endpointOrigin, origin)
	}
	return parsed.String(), nil
}

func mastodonVersionSupportsPKCE(raw string) bool {
	parts := strings.SplitN(strings.TrimSpace(raw), ".", 3)
	if len(parts) < 2 {
		return false
	}
	major, errMajor := strconv.Atoi(parts[0])
	minor, errMinor := strconv.Atoi(parts[1])
	if errMajor != nil || errMinor != nil {
		return false
	}
	return major > 4 || (major == 4 && minor >= 3)
}

// ResolveTypedSecretRef is the only Mastodon import resolver. It preserves
// the shared runtime resolver's env/op/keychain support while rejecting the
// legacy literal-value fallback for this new credential boundary.
func ResolveTypedSecretRef(ctx context.Context, ref string) (string, error) {
	if err := ValidateSecretRef(ref); err != nil {
		return "", err
	}
	return runtimeenv.ResolveSecretRef(ctx, ref)
}
