package xapi

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"strings"
	"syscall"

	"github.com/steipete/sweetcookie"
)

type cookieLoader func(context.Context, sweetcookie.Options) (sweetcookie.Result, error)

func resolveCookies(ctx context.Context, opts Options) (string, string, error) {
	return resolveCookiesWithLoader(ctx, opts, sweetcookie.Get)
}

func resolveCookiesWithLoader(ctx context.Context, opts Options, load cookieLoader) (string, string, error) {
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

	result, err := load(ctx, sweetcookie.Options{
		URL:      "https://x.com/",
		Names:    []string{"ct0", "auth_token"},
		Browsers: browsers,
		Profiles: profiles,
		Mode:     sweetcookie.ModeFirst,
		Timeout:  opts.Timeout,
	})
	if err != nil {
		if isCookieStorePermissionError(err) {
			return "", "", browserCookieAccessDenied(err)
		}
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
		if hasCookieStorePermissionWarning(result.Warnings) {
			return "", "", browserCookieAccessDenied(errors.New(strings.Join(result.Warnings, " | ")))
		}
		msg := "could not find ct0 cookie for x.com in local browser profiles"
		if len(result.Warnings) > 0 {
			msg += ": " + strings.Join(result.Warnings, " | ")
		}
		return "", "", errors.New(msg)
	}

	return ct0, authToken, nil
}

func isCookieStorePermissionError(err error) bool {
	return errors.Is(err, fs.ErrPermission) || errors.Is(err, syscall.EPERM) || errors.Is(err, syscall.EACCES)
}

func hasCookieStorePermissionWarning(warnings []string) bool {
	for _, warning := range warnings {
		warning = strings.ToLower(warning)
		denied := strings.Contains(warning, "operation not permitted") || strings.Contains(warning, "permission denied") || strings.Contains(warning, "access denied")
		if !denied {
			continue
		}
		// Match the reader's store failures, not the "cookie" in its
		// sweetcookie prefix or an unrelated Keychain/decryption warning.
		message := strings.TrimSpace(strings.TrimPrefix(warning, "sweetcookie:"))
		if strings.HasPrefix(message, "failed to copy cookies db:") ||
			(strings.HasPrefix(message, "failed to open ") && strings.Contains(message, " cookies db:")) ||
			(strings.HasPrefix(message, "failed to read ") && strings.Contains(message, " cookies:")) ||
			strings.HasPrefix(message, "safari read failed:") {
			return true
		}
	}
	return false
}

func browserCookieAccessDenied(err error) error {
	const guidance = "Check that this process can read the selected browser profile. On macOS, grant Full Disk Access to the app or executable that runs this command (for example Rex or the Rex Server helper) in System Settings > Privacy & Security > Full Disk Access. Quit and reopen that exact app or helper before retrying. dbrain cannot grant Full Disk Access automatically."
	return fmt.Errorf("browser cookie access denied: %w\n\n%s", err, guidance)
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
