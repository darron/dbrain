package entities

import (
	"net/url"
	"strings"
)

func entityMatches(entity Entity, query string) bool {
	candidates := []string{entity.Key, entity.Name, entity.CanonicalURL, entity.Domain}
	candidates = append(candidates, entity.Aliases...)
	for _, candidate := range candidates {
		if strings.Contains(strings.ToLower(strings.TrimSpace(candidate)), query) {
			return true
		}
	}
	return false
}

func normalizeKind(value string) string {
	value = strings.ToLower(strings.TrimSpace(value))
	switch value {
	case "person", "org", "project", "site":
		return value
	default:
		return ""
	}
}

func parseGitHubRepo(raw string) (string, string, bool) {
	u, err := url.Parse(strings.TrimSpace(raw))
	if err != nil {
		return "", "", false
	}
	host := normalizeDomain(u.Hostname())
	if host != "github.com" {
		return "", "", false
	}
	parts := splitPath(u.Path)
	if len(parts) < 2 {
		return "", "", false
	}
	return parts[0], parts[1], true
}

func splitPath(path string) []string {
	raw := strings.Split(strings.Trim(path, "/"), "/")
	result := make([]string, 0, len(raw))
	for _, part := range raw {
		part = strings.TrimSpace(part)
		if part != "" {
			result = append(result, part)
		}
	}
	return result
}

func brandTokenFromURL(raw string) string {
	u, err := url.Parse(strings.TrimSpace(raw))
	if err != nil {
		return ""
	}
	return domainBrandToken(u.Hostname())
}

func normalizeDomain(value string) string {
	value = strings.ToLower(strings.TrimSpace(value))
	value = strings.TrimPrefix(value, "www.")
	return value
}

func domainBrandToken(value string) string {
	domain := normalizeDomain(value)
	if domain == "" {
		return ""
	}
	if isGenericIdentityHost(domain) {
		return ""
	}
	parts := strings.Split(domain, ".")
	if len(parts) == 0 {
		return ""
	}
	if len(parts) >= 3 && strings.HasSuffix(domain, ".github.io") {
		return normalizeIdentityToken(parts[len(parts)-3])
	}
	if len(parts) == 1 {
		return normalizeIdentityToken(parts[0])
	}

	idx := len(parts) - 2
	if idx > 0 && isGenericDomainLabel(parts[idx]) {
		idx--
	}
	if idx < 0 || idx >= len(parts) {
		return ""
	}
	return normalizeIdentityToken(parts[idx])
}

func isGenericDomainLabel(value string) bool {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "co", "com", "net", "org", "gov", "edu", "ac":
		return true
	default:
		return false
	}
}

func isGenericIdentityHost(domain string) bool {
	switch normalizeDomain(domain) {
	case "", "github.com", "github.io", "x.com", "twitter.com", "youtube.com", "youtu.be":
		return true
	default:
		return false
	}
}

func isGenericSiteDomain(domain string) bool {
	switch normalizeDomain(domain) {
	case "", "github.com", "x.com", "twitter.com", "youtube.com", "youtu.be":
		return true
	default:
		return false
	}
}

func siteRootURL(raw, domain string) string {
	u, err := url.Parse(strings.TrimSpace(raw))
	if err != nil {
		return "https://" + domain
	}
	scheme := strings.TrimSpace(u.Scheme)
	if scheme == "" {
		scheme = "https"
	}
	return scheme + "://" + domain
}

func entitySlug(value string) string {
	value = strings.ToLower(strings.TrimSpace(value))
	var b strings.Builder
	lastDash := false
	for _, r := range value {
		switch {
		case r >= 'a' && r <= 'z':
			b.WriteRune(r)
			lastDash = false
		case r >= '0' && r <= '9':
			b.WriteRune(r)
			lastDash = false
		default:
			if !lastDash && b.Len() > 0 {
				b.WriteRune('-')
				lastDash = true
			}
		}
	}
	slug := strings.Trim(b.String(), "-")
	if slug == "" {
		return "entity"
	}
	return slug
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if trimmed := strings.TrimSpace(value); trimmed != "" {
			return trimmed
		}
	}
	return ""
}
