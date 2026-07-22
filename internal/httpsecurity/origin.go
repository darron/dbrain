package httpsecurity

import (
	"net/http"
	"net/url"
	"strings"
)

// OriginGuard blocks unsafe cross-origin browser requests while preserving
// non-browser clients and the browser-extension link-add integration.
func OriginGuard(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet || r.Method == http.MethodHead {
			next.ServeHTTP(w, r)
			return
		}
		origin := strings.TrimSpace(r.Header.Get("Origin"))
		if origin == "" || sameOrigin(origin, requestOrigin(r)) {
			next.ServeHTTP(w, r)
			return
		}
		if browserExtensionLinkAddOrigin(origin, r.URL.Path) {
			next.ServeHTTP(w, r)
			return
		}
		http.Error(w, "forbidden origin", http.StatusForbidden)
	})
}

func browserExtensionLinkAddOrigin(origin string, requestPath string) bool {
	if requestPath != "/api/links" {
		return false
	}
	parsed, err := url.Parse(strings.TrimSpace(origin))
	if err != nil || !validOriginURL(parsed) {
		return false
	}
	switch strings.ToLower(parsed.Scheme) {
	case "chrome-extension", "safari-web-extension":
		return parsed.Host != ""
	default:
		return false
	}
}

func sameOrigin(origin string, expected string) bool {
	parsed, err := url.Parse(origin)
	if err != nil || !validOriginURL(parsed) {
		return false
	}
	return strings.EqualFold(normalizeOrigin(parsed.Scheme+"://"+parsed.Host), normalizeOrigin(expected))
}

func validOriginURL(parsed *url.URL) bool {
	return parsed != nil &&
		parsed.Scheme != "" &&
		parsed.Host != "" &&
		parsed.User == nil &&
		parsed.Opaque == "" &&
		parsed.Path == "" &&
		parsed.RawPath == "" &&
		parsed.RawQuery == "" &&
		!parsed.ForceQuery &&
		parsed.Fragment == ""
}

func requestOrigin(r *http.Request) string {
	scheme := "http"
	if r.TLS != nil {
		scheme = "https"
	}
	return scheme + "://" + r.Host
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
