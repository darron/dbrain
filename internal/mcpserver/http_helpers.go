package mcpserver

import (
	"net"
	"net/http"
	"net/url"
	"strings"
)

func requestEndpointURL(r *http.Request) string {
	if r == nil {
		return DefaultHTTPPath
	}
	if strings.TrimSpace(r.Host) == "" {
		return r.URL.Path
	}
	scheme := "http"
	if r.TLS != nil {
		scheme = "https"
	}
	return scheme + "://" + r.Host + r.URL.Path
}

func normalizeHTTPPath(path string) string {
	path = strings.TrimSpace(path)
	if path == "" {
		return DefaultHTTPPath
	}
	if !strings.HasPrefix(path, "/") {
		path = "/" + path
	}
	return path
}

func originAllowed(r *http.Request, allowed []string) bool {
	origin := strings.TrimSpace(r.Header.Get("Origin"))
	if origin == "" {
		return true
	}
	if originMatchesHost(origin, r.Host) {
		return true
	}
	normalizedOrigin := normalizeOrigin(origin)
	for _, entry := range allowed {
		entry = strings.TrimSpace(entry)
		if entry == "*" {
			return true
		}
		if normalizeOrigin(entry) == normalizedOrigin {
			return true
		}
	}
	return false
}

func originMatchesHost(origin string, host string) bool {
	parsed, err := url.Parse(origin)
	if err != nil || parsed.Host == "" {
		return false
	}
	originHost := strings.ToLower(parsed.Host)
	requestHost := strings.ToLower(host)
	if originHost == requestHost {
		return true
	}
	originName, originPort, errOrigin := net.SplitHostPort(originHost)
	requestName, requestPort, errRequest := net.SplitHostPort(requestHost)
	if errOrigin == nil && errRequest == nil && originPort == requestPort {
		return strings.EqualFold(originName, requestName)
	}
	return false
}

func normalizeOrigin(origin string) string {
	origin = strings.TrimRight(strings.TrimSpace(origin), "/")
	parsed, err := url.Parse(origin)
	if err != nil || parsed.Scheme == "" || parsed.Host == "" {
		return origin
	}
	parsed.Scheme = strings.ToLower(parsed.Scheme)
	parsed.Host = strings.ToLower(parsed.Host)
	parsed.Path = ""
	parsed.RawQuery = ""
	parsed.Fragment = ""
	return parsed.String()
}

func defaultString(value string, fallback string) string {
	if strings.TrimSpace(value) != "" {
		return strings.TrimSpace(value)
	}
	return fallback
}
