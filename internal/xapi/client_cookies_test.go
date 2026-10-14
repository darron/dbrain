package xapi

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/steipete/sweetcookie"
)

func TestResolveCookiesWithLoaderReportsBrowserStorePermissionWarning(t *testing.T) {
	t.Parallel()

	for _, warning := range []string{
		"sweetcookie: failed to copy cookies DB: open /Users/example/Library/Application Support/Google/Chrome/Default/Cookies: operation not permitted",
		"sweetcookie: failed to open Firefox cookies DB: permission denied",
		"sweetcookie: failed to read Chrome cookies: permission denied",
		"sweetcookie: Safari read failed: open /Users/example/Library/Cookies/Cookies.binarycookies: operation not permitted",
	} {
		t.Run(warning, func(t *testing.T) {
			_, _, err := resolveCookiesWithLoader(t.Context(), Options{Browser: "chrome"}, func(context.Context, sweetcookie.Options) (sweetcookie.Result, error) {
				return sweetcookie.Result{Warnings: []string{warning}}, nil
			})
			if err == nil {
				t.Fatal("resolveCookiesWithLoader() error = nil, want browser cookie access denial")
			}
			for _, want := range []string{
				"browser cookie access denied",
				"System Settings > Privacy & Security > Full Disk Access",
				"Rex or the Rex Server helper",
				"Quit and reopen",
				"cannot grant Full Disk Access automatically",
				warning,
			} {
				if !strings.Contains(err.Error(), want) {
					t.Errorf("resolveCookiesWithLoader() error = %q, want %q", err, want)
				}
			}
			if strings.Contains(err.Error(), "could not find ct0") {
				t.Errorf("permission denial misreported as missing cookie: %v", err)
			}
		})
	}
}

func TestResolveCookiesWithLoaderKeepsMissingCookieDiagnostic(t *testing.T) {
	t.Parallel()

	_, _, err := resolveCookiesWithLoader(t.Context(), Options{}, func(context.Context, sweetcookie.Options) (sweetcookie.Result, error) {
		return sweetcookie.Result{}, nil
	})
	if err == nil || err.Error() != "could not find ct0 cookie for x.com in local browser profiles" {
		t.Fatalf("missing-cookie diagnostic = %v", err)
	}
}

func TestResolveCookiesWithLoaderKeepsOtherErrorGuidance(t *testing.T) {
	t.Parallel()

	cause := errors.New("Keychain request failed")
	_, _, err := resolveCookiesWithLoader(t.Context(), Options{}, func(context.Context, sweetcookie.Options) (sweetcookie.Result, error) {
		return sweetcookie.Result{}, cause
	})
	if !errors.Is(err, cause) || !strings.Contains(err.Error(), "Keychain prompt") || strings.Contains(err.Error(), "Full Disk Access") {
		t.Fatalf("other cookie-reader error = %v", err)
	}
}

func TestResolveCookiesWithLoaderPreservesPermissionError(t *testing.T) {
	t.Parallel()

	for _, permissionErr := range []error{syscall.EPERM, syscall.EACCES} {
		permissionErr := permissionErr
		t.Run(permissionErr.Error(), func(t *testing.T) {
			_, _, err := resolveCookiesWithLoader(t.Context(), Options{}, func(context.Context, sweetcookie.Options) (sweetcookie.Result, error) {
				return sweetcookie.Result{}, fmt.Errorf("copy cookie database: %w", permissionErr)
			})
			if err == nil {
				t.Fatal("resolveCookiesWithLoader() error = nil, want permission denial")
			}
			if !errors.Is(err, permissionErr) {
				t.Errorf("resolveCookiesWithLoader() error = %v, want errors.Is(err, %v)", err, permissionErr)
			}
			if !strings.Contains(err.Error(), "browser cookie access denied") {
				t.Errorf("resolveCookiesWithLoader() error = %q, want browser cookie access denial", err)
			}
		})
	}
}

func TestResolveCookiesWithLoaderDoesNotMistakeKeychainWarningForBrowserAccess(t *testing.T) {
	t.Parallel()

	for _, warning := range []string{
		"sweetcookie: Keychain operation not permitted",
		"sweetcookie: failed to read Keychain: permission denied",
		"sweetcookie: macOS keychain read failed (Chrome Safe Storage): operation not permitted",
	} {
		t.Run(warning, func(t *testing.T) {
			_, _, err := resolveCookiesWithLoader(t.Context(), Options{}, func(context.Context, sweetcookie.Options) (sweetcookie.Result, error) {
				return sweetcookie.Result{Warnings: []string{warning}}, nil
			})
			if err == nil {
				t.Fatal("resolveCookiesWithLoader() error = nil, want missing cookie diagnostic")
			}
			if strings.Contains(err.Error(), "browser cookie access denied") {
				t.Errorf("resolveCookiesWithLoader() error = %q, must retain the Keychain diagnostic", err)
			}
			if !strings.Contains(err.Error(), "could not find ct0 cookie") || !strings.Contains(err.Error(), warning) {
				t.Errorf("resolveCookiesWithLoader() error = %q, want missing cookie and original Keychain diagnostic", err)
			}
		})
	}
}

func TestResolveCookiesWithLoaderAcceptsCookieDespiteOtherProfilePermissionWarning(t *testing.T) {
	t.Parallel()

	ct0, authToken, err := resolveCookiesWithLoader(t.Context(), Options{}, func(context.Context, sweetcookie.Options) (sweetcookie.Result, error) {
		return sweetcookie.Result{
			Cookies:  []sweetcookie.Cookie{{Name: "ct0", Value: "csrf"}, {Name: "auth_token", Value: "auth"}},
			Warnings: []string{"sweetcookie: failed to read Chrome cookies: operation not permitted"},
		}, nil
	})
	if err != nil {
		t.Fatalf("resolveCookiesWithLoader() error = %v, want nil", err)
	}
	if ct0 != "csrf" || authToken != "auth" {
		t.Errorf("resolveCookiesWithLoader() = (%q, %q), want (%q, %q)", ct0, authToken, "csrf", "auth")
	}
}

func TestResolveCookiesWithLoaderPassesBrowserProfileAndTimeout(t *testing.T) {
	t.Parallel()

	var got sweetcookie.Options
	_, _, err := resolveCookiesWithLoader(t.Context(), Options{
		Browser: "chrome, firefox",
		Profile: "Profile 7",
		Timeout: 9 * time.Second,
	}, func(_ context.Context, opts sweetcookie.Options) (sweetcookie.Result, error) {
		got = opts
		return sweetcookie.Result{Cookies: []sweetcookie.Cookie{{Name: "ct0", Value: "csrf"}}}, nil
	})
	if err != nil {
		t.Fatalf("resolveCookiesWithLoader() error = %v", err)
	}
	if got.URL != "https://x.com/" || got.Mode != sweetcookie.ModeFirst || got.Timeout != 9*time.Second {
		t.Errorf("loader options = %#v, want X URL, ModeFirst, and supplied timeout", got)
	}
	if got.Browsers == nil || len(got.Browsers) != 2 || got.Browsers[0] != sweetcookie.BrowserChrome || got.Browsers[1] != sweetcookie.BrowserFirefox {
		t.Errorf("loader browsers = %#v, want [chrome firefox]", got.Browsers)
	}
	if got.Profiles[sweetcookie.BrowserChrome] != "Profile 7" {
		t.Errorf("loader profile = %#v, want Chrome Profile 7", got.Profiles)
	}
}
