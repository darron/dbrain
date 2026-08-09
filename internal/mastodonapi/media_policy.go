package mastodonapi

import (
	"fmt"
	"net/url"
	"strings"

	"github.com/darron/dbrain/internal/safehttp"
)

// MediaHTTPPolicy reconstructs the exact origin authority for one Mastodon
// media URL. Callers may inject transport settings for tests, but cannot widen
// the origin or disable credential-query redirect rejection.
func MediaHTTPPolicy(rawURL string, basePolicy *safehttp.Policy) (safehttp.Policy, error) {
	target, err := url.Parse(strings.TrimSpace(rawURL))
	if err != nil || target == nil || target.Opaque != "" || target.User != nil || target.Hostname() == "" || strings.ToLower(target.Scheme) != "https" {
		return safehttp.Policy{}, &safehttp.PolicyError{Reason: "Mastodon media URL has no safe origin"}
	}
	originURL := (&url.URL{Scheme: target.Scheme, Host: target.Host}).String()
	origin, err := safehttp.CanonicalOriginEndpoint(originURL)
	if err != nil {
		return safehttp.Policy{}, fmt.Errorf("reconstruct Mastodon media origin: %w", err)
	}

	policy := safehttp.Policy{}
	if basePolicy != nil {
		policy = *basePolicy
	}
	policy.AllowedOrigins = []string{origin}
	policy.RejectCredentialQueryOnRedirect = true
	return policy, nil
}
