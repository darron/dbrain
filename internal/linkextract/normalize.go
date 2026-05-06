package linkextract

import (
	"net"
	neturl "net/url"
	"path"
	"strings"

	"github.com/darron/dbrain/internal/model"
	"github.com/darron/dbrain/internal/vault"
)

func NormalizeCandidate(raw string) (model.SourceCandidate, bool) {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return model.SourceCandidate{}, false
	}

	u, err := neturl.Parse(trimmed)
	if err != nil || u.Host == "" {
		return model.SourceCandidate{}, false
	}

	hostName := strings.ToLower(u.Hostname())
	hostName = strings.TrimPrefix(hostName, "www.")
	hostName = strings.TrimPrefix(hostName, "m.")
	switch hostName {
	case "twitter.com", "mobile.twitter.com":
		hostName = "x.com"
	}
	host := hostName
	if port := strings.TrimSpace(u.Port()); port != "" {
		host = hostName + ":" + port
	}

	sourceType := classifySourceType(hostName, u.Path)
	if sourceType == "skip" {
		return model.SourceCandidate{}, false
	}

	scheme := strings.ToLower(u.Scheme)
	if scheme == "" {
		scheme = "https"
	}
	if scheme != "http" && scheme != "https" {
		return model.SourceCandidate{}, false
	}
	if hostName != "localhost" && net.ParseIP(hostName) == nil {
		scheme = "https"
	}

	cleanedPath := path.Clean("/" + strings.TrimSpace(u.EscapedPath()))
	if cleanedPath == "/." {
		cleanedPath = "/"
	}
	u = &neturl.URL{
		Scheme: scheme,
		Host:   host,
		Path:   cleanedPath,
	}

	query := filterQueryParams(trimmed, hostName)
	u.RawQuery = query.Encode()
	u.Fragment = ""

	canonical := strings.TrimSuffix(u.String(), "?")
	if hostName == "github.com" {
		canonical = trimGitHubURL(canonical)
	}

	keyHash := shortHash(canonical)
	slugBase := hostName
	if slugBase == "" {
		slugBase = sourceType
	}
	noteSlug := slugify(slugBase + "-" + keyHash)

	return model.SourceCandidate{
		OriginalURL:   trimmed,
		CanonicalURL:  canonical,
		NormalizedURL: canonical,
		SourceType:    sourceType,
		Domain:        hostName,
		SourceKey:     "src:" + keyHash,
		NotePath:      vault.SourceNoteRelativePath(sourceType, noteSlug),
	}, true
}

func classifySourceType(host, urlPath string) string {
	switch host {
	case "t.co", "pbs.twimg.com", "video.twimg.com":
		return "skip"
	case "x.com":
		if strings.HasPrefix(urlPath, "/i/article/") {
			return "x_article"
		}
		return "skip"
	case "github.com":
		return "github"
	case "youtu.be", "youtube.com", "www.youtube.com":
		return "youtube"
	}
	if strings.HasSuffix(strings.ToLower(urlPath), ".pdf") {
		return "pdf"
	}
	return "web"
}

func filterQueryParams(raw, host string) neturl.Values {
	u, err := neturl.Parse(raw)
	if err != nil {
		return neturl.Values{}
	}
	values := u.Query()
	filtered := neturl.Values{}
	for key, vals := range values {
		lower := strings.ToLower(key)
		if strings.HasPrefix(lower, "utm_") || lower == "fbclid" || lower == "gclid" || lower == "igshid" || lower == "ref_src" || lower == "si" {
			continue
		}
		if host == "youtube.com" || host == "youtu.be" || host == "www.youtube.com" {
			if lower == "t" || lower == "start" || lower == "feature" {
				continue
			}
		}
		for _, value := range vals {
			filtered.Add(key, value)
		}
	}
	return filtered
}

func trimGitHubURL(value string) string {
	u, err := neturl.Parse(value)
	if err != nil {
		return value
	}
	parts := strings.Split(strings.Trim(u.Path, "/"), "/")
	if len(parts) >= 2 {
		u.Path = "/" + parts[0] + "/" + parts[1]
		u.RawQuery = ""
		u.Fragment = ""
	}
	return u.String()
}
