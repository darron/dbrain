package sourceenrich

import (
	"context"
	"encoding/base64"
	"fmt"
	"net/http"
	"net/http/cookiejar"
	"regexp"
	"strings"

	"github.com/darron/dbrain/internal/model"
)

var sucuriEncodedScriptPattern = regexp.MustCompile(`\bS\s*=\s*(?:'([^']+)'|"([^"]+)")`)

func extractProtectedSource(ctx context.Context, rawURL string) (model.ExtractResult, bool, error) {
	jar, err := cookiejar.New(nil)
	if err != nil {
		return model.ExtractResult{}, false, fmt.Errorf("create cookie jar: %w", err)
	}

	client := &http.Client{Jar: jar}

	challengeResp, challengeBody, err := fetchHTTPText(ctx, client, rawURL)
	if err != nil {
		return model.ExtractResult{}, false, fmt.Errorf("fetch protected source challenge: %w", err)
	}
	if !looksLikeSucuriChallenge(challengeResp, challengeBody) {
		return model.ExtractResult{}, false, nil
	}

	cookie, err := solveSucuriChallengeCookie(challengeBody)
	if err != nil {
		return model.ExtractResult{}, false, fmt.Errorf("solve sucuri challenge: %w", err)
	}
	if challengeResp.Request == nil || challengeResp.Request.URL == nil {
		return model.ExtractResult{}, false, fmt.Errorf("sucuri challenge response missing request url")
	}
	jar.SetCookies(challengeResp.Request.URL, []*http.Cookie{cookie})

	protectedResp, protectedBody, err := fetchHTTPText(ctx, client, rawURL)
	if err != nil {
		return model.ExtractResult{}, false, fmt.Errorf("refetch protected source: %w", err)
	}
	if protectedResp.StatusCode < 200 || protectedResp.StatusCode >= 300 {
		return model.ExtractResult{}, false, fmt.Errorf("refetch protected source: unexpected status %d", protectedResp.StatusCode)
	}

	if wpJSONURL := wordpressJSONURL(protectedResp); wpJSONURL != "" {
		if extract, ok, err := fetchWordPressJSONExtract(ctx, client, rawURL, wpJSONURL); err == nil && ok {
			return extract, true, nil
		}
	}

	return extractHTMLSource(rawURL, protectedResp, protectedBody, "sucuri-html", protectedFetchToolVersion, "sucuri"), true, nil
}

func looksLikeSucuriChallenge(resp *http.Response, body string) bool {
	if resp == nil {
		return false
	}
	value := strings.ToLower(body)
	server := strings.ToLower(resp.Header.Get("server"))
	return strings.Contains(server, "sucuri") &&
		strings.Contains(value, "javascript is required") &&
		strings.Contains(value, "sucuri_cloudproxy_js") &&
		strings.Contains(value, "e(r);")
}

func solveSucuriChallengeCookie(challengeHTML string) (*http.Cookie, error) {
	match := sucuriEncodedScriptPattern.FindStringSubmatch(challengeHTML)
	if len(match) != 3 {
		return nil, fmt.Errorf("sucuri challenge payload not found")
	}
	encoded := firstNonEmpty(match[1], match[2])
	if encoded == "" {
		return nil, fmt.Errorf("sucuri challenge payload empty")
	}

	decoded, err := base64.StdEncoding.DecodeString(strings.TrimSpace(encoded))
	if err != nil {
		return nil, fmt.Errorf("decode sucuri payload: %w", err)
	}

	vars := map[string]string{}
	for _, stmt := range splitJSStatements(string(decoded)) {
		stmt = strings.TrimSpace(stmt)
		if stmt == "" {
			continue
		}
		if strings.HasPrefix(stmt, "document.cookie") {
			index := strings.Index(stmt, "=")
			if index < 0 {
				continue
			}
			value, err := evalJSConcatExpression(strings.TrimSpace(stmt[index+1:]), vars)
			if err != nil {
				return nil, fmt.Errorf("evaluate sucuri cookie expression: %w", err)
			}
			cookie, err := parseDocumentCookie(value)
			if err != nil {
				return nil, err
			}
			return cookie, nil
		}

		index := strings.Index(stmt, "=")
		if index < 0 {
			continue
		}
		name := strings.TrimSpace(stmt[:index])
		name = strings.TrimPrefix(name, "var ")
		if name == "" || strings.Contains(name, ".") {
			continue
		}
		value, err := evalJSConcatExpression(strings.TrimSpace(stmt[index+1:]), vars)
		if err != nil {
			return nil, fmt.Errorf("evaluate sucuri variable %s: %w", name, err)
		}
		vars[name] = value
	}

	return nil, fmt.Errorf("sucuri cookie assignment not found")
}

func parseDocumentCookie(value string) (*http.Cookie, error) {
	resp := &http.Response{Header: make(http.Header)}
	resp.Header.Add("Set-Cookie", value)
	cookies := resp.Cookies()
	if len(cookies) == 0 {
		return nil, fmt.Errorf("parse sucuri cookie")
	}
	return cookies[0], nil
}
