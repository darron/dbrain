package feedimport

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"net"
	neturl "net/url"
	"path"
	"regexp"
	"strings"
)

var nonSlug = regexp.MustCompile(`[^a-z0-9]+`)

func NormalizeFeedURL(raw string) (string, string, error) {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return "", "", fmt.Errorf("feed URL is required")
	}
	u, err := neturl.Parse(trimmed)
	if err != nil {
		return "", "", fmt.Errorf("parse feed URL: %w", err)
	}
	if u.Scheme == "" {
		u.Scheme = "https"
	}
	u.Scheme = strings.ToLower(u.Scheme)
	if u.Scheme != "http" && u.Scheme != "https" {
		return "", "", fmt.Errorf("feed URL must use http or https")
	}
	hostName := strings.ToLower(strings.TrimSpace(u.Hostname()))
	if hostName == "" {
		return "", "", fmt.Errorf("feed URL host is required")
	}
	host := hostName
	if port := strings.TrimSpace(u.Port()); port != "" {
		host = net.JoinHostPort(hostName, port)
	}
	cleanedPath := path.Clean("/" + strings.TrimSpace(u.EscapedPath()))
	if cleanedPath == "/." {
		cleanedPath = "/"
	}
	u.Host = host
	u.Path = cleanedPath
	u.Fragment = ""
	normalized := strings.TrimSuffix(u.String(), "?")
	return normalized, "feed:" + shortHash(normalized), nil
}

func shortHash(value string) string {
	sum := sha256.Sum256([]byte(value))
	return hex.EncodeToString(sum[:])[:12]
}

func slugify(value string) string {
	value = strings.ToLower(strings.TrimSpace(value))
	value = nonSlug.ReplaceAllString(value, "-")
	value = strings.Trim(value, "-")
	if value == "" {
		return "feed-entry"
	}
	if len(value) > 80 {
		value = strings.Trim(value[:80], "-")
	}
	if value == "" {
		return "feed-entry"
	}
	return value
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value != "" {
			return value
		}
	}
	return ""
}
