package remote

import (
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"regexp"
	"strings"
	"sync"
	"time"

	"tailscale.com/client/tailscale/apitype"
)

var urlPattern = regexp.MustCompile(`https?://[^\s]+`)

const identityCacheTTL = 30 * time.Second

func identityLogger(client whoIsClient, next http.Handler, logOut io.Writer) http.Handler {
	var mu sync.Mutex
	cache := map[string]identityCacheEntry{}
	errorLogUntil := map[string]time.Time{}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		identity := r.RemoteAddr
		cacheKey := identityCacheKey(r.RemoteAddr)
		now := time.Now()
		mu.Lock()
		cached, ok := cache[cacheKey]
		if ok && now.Before(cached.expires) {
			identity = cached.identity
		}
		mu.Unlock()
		if !ok || !now.Before(cached.expires) {
			if who, err := client.WhoIs(r.Context(), r.RemoteAddr); err == nil {
				identity = whoIsLabel(who, r.RemoteAddr)
				mu.Lock()
				cache[cacheKey] = identityCacheEntry{identity: identity, expires: now.Add(identityCacheTTL)}
				mu.Unlock()
			} else {
				shouldLog := false
				mu.Lock()
				if !now.Before(errorLogUntil[cacheKey]) {
					errorLogUntil[cacheKey] = now.Add(identityCacheTTL)
					shouldLog = true
				}
				mu.Unlock()
				if shouldLog {
					logWhoIsFailure(logOut, r.RemoteAddr, err)
				}
			}
		}
		logRemoteRequest(logOut, r, identity)
		next.ServeHTTP(w, r)
	})
}

type identityCacheEntry struct {
	identity string
	expires  time.Time
}

func identityCacheKey(remoteAddr string) string {
	host, _, err := net.SplitHostPort(remoteAddr)
	if err != nil || strings.TrimSpace(host) == "" {
		return remoteAddr
	}
	return host
}

func logWhoIsFailure(out io.Writer, remoteAddr string, err error) {
	if out == nil {
		out = os.Stderr
	}
	_, _ = fmt.Fprintf(out, "WARNING tsnet WhoIs failed remote=%q error=%v\n", remoteAddr, err)
}

func whoIsLabel(who *apitype.WhoIsResponse, fallback string) string {
	if who == nil {
		return fallback
	}
	if who.UserProfile != nil {
		if strings.TrimSpace(who.UserProfile.LoginName) != "" {
			return who.UserProfile.LoginName
		}
		if strings.TrimSpace(who.UserProfile.DisplayName) != "" {
			return who.UserProfile.DisplayName
		}
	}
	if who.Node != nil && strings.TrimSpace(who.Node.Name) != "" {
		return strings.TrimSuffix(who.Node.Name, ".")
	}
	return fallback
}

func logRemoteRequest(out io.Writer, r *http.Request, identity string) {
	if out == nil {
		out = os.Stderr
	}
	_, _ = fmt.Fprintf(out, "DEBUG %s remote request method=%s path=%s identity=%q remote=%q\n", time.Now().Format("15:04:05.000"), r.Method, r.URL.Path, identity, r.RemoteAddr)
}

func newUserLogger(out io.Writer) func(string, ...any) {
	var mu sync.Mutex
	seenLines := map[string]bool{}
	seenURLs := map[string]bool{}
	return func(format string, args ...any) {
		line := fmt.Sprintf(format, args...)
		lineKey := strings.TrimSpace(line)
		mu.Lock()
		defer mu.Unlock()
		if seenLines[lineKey] {
			return
		}
		seenLines[lineKey] = true
		_, _ = fmt.Fprintln(out, line)
		for _, url := range urlPattern.FindAllString(line, -1) {
			url = strings.TrimRight(url, ".,);]")
			if seenURLs[url] {
				continue
			}
			seenURLs[url] = true
			_, _ = fmt.Fprintf(out, "Visit this URL to authenticate dbrain:\n%s\n", url)
		}
	}
}
