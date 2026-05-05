package xapi

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/steipete/sweetcookie"
)

func resolveCookies(ctx context.Context, opts Options) (string, string, error) {
	profiles := map[sweetcookie.Browser]string{}
	browsers, err := parseBrowsers(opts.Browser)
	if err != nil {
		return "", "", err
	}
	if opts.Profile != "" {
		if len(browsers) == 0 {
			return "", "", fmt.Errorf("--profile requires --browser so the profile target is unambiguous")
		}
		profiles[browsers[0]] = opts.Profile
	}

	result, err := sweetcookie.Get(ctx, sweetcookie.Options{
		URL:      "https://x.com/",
		Names:    []string{"ct0", "auth_token"},
		Browsers: browsers,
		Profiles: profiles,
		Mode:     sweetcookie.ModeFirst,
		Timeout:  opts.Timeout,
	})
	if err != nil {
		return "", "", fmt.Errorf(
			"load browser cookies: %w\n\nIf macOS shows a Keychain prompt for Go or dbrain, choose Allow or Always Allow\nYou can also bypass browser lookup with --ct0 and --auth-token",
			err,
		)
	}

	var ct0 string
	var authToken string
	for _, cookie := range result.Cookies {
		switch cookie.Name {
		case "ct0":
			if ct0 == "" {
				ct0 = cookie.Value
			}
		case "auth_token":
			if authToken == "" {
				authToken = cookie.Value
			}
		}
	}

	if ct0 == "" {
		msg := "could not find ct0 cookie for x.com in local browser profiles"
		if len(result.Warnings) > 0 {
			msg += ": " + strings.Join(result.Warnings, " | ")
		}
		return "", "", errors.New(msg)
	}

	return ct0, authToken, nil
}

func parseBrowsers(value string) ([]sweetcookie.Browser, error) {
	value = strings.TrimSpace(value)
	if value == "" {
		return nil, nil
	}

	parts := strings.Split(value, ",")
	result := make([]sweetcookie.Browser, 0, len(parts))
	for _, part := range parts {
		switch strings.ToLower(strings.TrimSpace(part)) {
		case "chrome":
			result = append(result, sweetcookie.BrowserChrome)
		case "chromium":
			result = append(result, sweetcookie.BrowserChromium)
		case "edge":
			result = append(result, sweetcookie.BrowserEdge)
		case "brave":
			result = append(result, sweetcookie.BrowserBrave)
		case "vivaldi":
			result = append(result, sweetcookie.BrowserVivaldi)
		case "opera":
			result = append(result, sweetcookie.BrowserOpera)
		case "firefox":
			result = append(result, sweetcookie.BrowserFirefox)
		case "safari":
			result = append(result, sweetcookie.BrowserSafari)
		default:
			return nil, fmt.Errorf("unsupported browser %q", strings.TrimSpace(part))
		}
	}
	return result, nil
}
