package safehttp

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"strings"
	"time"
)

const defaultMaxRedirects = 10

type Policy struct {
	Timeout               time.Duration
	MaxRedirects          int
	AllowPrivateNetwork   bool
	AllowedPrivateOrigins []string
	// AllowedOrigins restricts every request and redirect to one of these exact
	// canonical origins. It is independent from private-network permission.
	AllowedOrigins     []string
	DisableCompression bool
	LookupNetIP        func(context.Context, string, string) ([]netip.Addr, error)
	DialContext        func(context.Context, string, string) (net.Conn, error)
	TLSClientConfig    *tls.Config
}

type PolicyError struct {
	Reason string
}

func (e *PolicyError) Error() string {
	return "safe HTTP policy: " + e.Reason
}

func IsPolicyError(err error) bool {
	var policyErr *PolicyError
	return errors.As(err, &policyErr)
}

type compiledPolicy struct {
	allowPrivateNetwork bool
	allowedPrivate      map[string]struct{}
	allowedOrigins      map[string]struct{}
	restrictOrigins     bool
	lookupNetIP         func(context.Context, string, string) ([]netip.Addr, error)
	dialContext         func(context.Context, string, string) (net.Conn, error)
}

type privateNetworkContextKey struct{}

type policyTransport struct {
	base   *http.Transport
	policy compiledPolicy
}

func NewClient(policy Policy) *http.Client {
	compiled := compilePolicy(policy)
	transport := &http.Transport{
		Proxy:              nil,
		DisableCompression: policy.DisableCompression,
		DialContext:        compiled.dial,
	}
	if policy.TLSClientConfig != nil {
		transport.TLSClientConfig = policy.TLSClientConfig.Clone()
	}

	maxRedirects := policy.MaxRedirects
	if maxRedirects <= 0 {
		maxRedirects = defaultMaxRedirects
	}
	return &http.Client{
		Timeout:   policy.Timeout,
		Transport: &policyTransport{base: transport, policy: compiled},
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			if len(via) > maxRedirects {
				return &PolicyError{Reason: fmt.Sprintf("stopped after %d redirects", maxRedirects)}
			}
			_, err := compiled.privateNetworkAllowed(req.URL)
			return err
		},
	}
}

func compilePolicy(policy Policy) compiledPolicy {
	lookupNetIP := policy.LookupNetIP
	if lookupNetIP == nil {
		lookupNetIP = net.DefaultResolver.LookupNetIP
	}
	dialContext := policy.DialContext
	if dialContext == nil {
		var dialer net.Dialer
		dialContext = dialer.DialContext
	}

	allowedPrivate := make(map[string]struct{}, len(policy.AllowedPrivateOrigins))
	for _, rawOrigin := range policy.AllowedPrivateOrigins {
		parsed, err := url.Parse(strings.TrimSpace(rawOrigin))
		if err != nil || parsed.User != nil {
			continue
		}
		origin, err := canonicalOrigin(parsed)
		if err == nil {
			allowedPrivate[origin] = struct{}{}
		}
	}
	allowedOrigins := make(map[string]struct{}, len(policy.AllowedOrigins))
	for _, rawOrigin := range policy.AllowedOrigins {
		origin, err := CanonicalOriginEndpoint(rawOrigin)
		if err == nil {
			allowedOrigins[origin] = struct{}{}
		}
	}
	return compiledPolicy{
		allowPrivateNetwork: policy.AllowPrivateNetwork,
		allowedPrivate:      allowedPrivate,
		allowedOrigins:      allowedOrigins,
		restrictOrigins:     len(policy.AllowedOrigins) > 0,
		lookupNetIP:         lookupNetIP,
		dialContext:         dialContext,
	}
}

func (t *policyTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	allowPrivate, err := t.policy.privateNetworkAllowed(req.URL)
	if err != nil {
		return nil, err
	}
	ctx := context.WithValue(req.Context(), privateNetworkContextKey{}, allowPrivate)
	return t.base.RoundTrip(req.Clone(ctx))
}

func (p compiledPolicy) privateNetworkAllowed(target *url.URL) (bool, error) {
	if target == nil {
		return false, &PolicyError{Reason: "request URL is missing"}
	}
	if target.Scheme != "http" && target.Scheme != "https" {
		return false, &PolicyError{Reason: fmt.Sprintf("URL scheme %q is not allowed", target.Scheme)}
	}
	if target.User != nil {
		return false, &PolicyError{Reason: "URL userinfo is not allowed"}
	}
	origin, err := canonicalOrigin(target)
	if err != nil {
		return false, err
	}
	if p.restrictOrigins {
		if _, allowed := p.allowedOrigins[origin]; !allowed {
			return false, &PolicyError{Reason: fmt.Sprintf("origin %s is not the configured origin", origin)}
		}
	}
	if p.allowPrivateNetwork {
		return true, nil
	}
	_, allowed := p.allowedPrivate[origin]
	return allowed, nil
}

// CanonicalOriginEndpoint validates a configured service endpoint as exactly
// one HTTP(S) origin. Paths, credentials, queries, and fragments are rejected
// so later SDK URL construction cannot silently broaden authority.
func CanonicalOriginEndpoint(raw string) (string, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return "", &PolicyError{Reason: "endpoint is required"}
	}
	target, err := url.Parse(raw)
	if err != nil {
		return "", &PolicyError{Reason: "endpoint is not a valid URL"}
	}
	if target.Opaque != "" || target.User != nil {
		return "", &PolicyError{Reason: "endpoint userinfo or opaque URLs are not allowed"}
	}
	if target.RawQuery != "" || target.ForceQuery || target.Fragment != "" {
		return "", &PolicyError{Reason: "endpoint query and fragment are not allowed"}
	}
	if target.Path != "" && target.Path != "/" || target.RawPath != "" {
		return "", &PolicyError{Reason: "endpoint must not contain a path"}
	}
	return canonicalOrigin(target)
}

func canonicalOrigin(target *url.URL) (string, error) {
	scheme := strings.ToLower(strings.TrimSpace(target.Scheme))
	host := strings.ToLower(strings.TrimSuffix(strings.TrimSpace(target.Hostname()), "."))
	if (scheme != "http" && scheme != "https") || host == "" {
		return "", &PolicyError{Reason: "request URL must have an HTTP or HTTPS origin"}
	}
	port := target.Port()
	if port == "" {
		if scheme == "https" {
			port = "443"
		} else {
			port = "80"
		}
	}
	return scheme + "://" + net.JoinHostPort(host, port), nil
}

func (p compiledPolicy) dial(ctx context.Context, network, address string) (net.Conn, error) {
	host, port, err := net.SplitHostPort(address)
	if err != nil {
		return nil, fmt.Errorf("safe HTTP policy: parse destination %q: %w", address, err)
	}
	host = strings.TrimSuffix(strings.ToLower(host), ".")

	var addrs []netip.Addr
	if literal, parseErr := netip.ParseAddr(host); parseErr == nil {
		addrs = []netip.Addr{literal}
	} else {
		lookupNetwork := "ip"
		if strings.HasSuffix(network, "4") {
			lookupNetwork = "ip4"
		} else if strings.HasSuffix(network, "6") {
			lookupNetwork = "ip6"
		}
		addrs, err = p.lookupNetIP(ctx, lookupNetwork, host)
		if err != nil {
			return nil, fmt.Errorf("resolve destination %s: %w", host, err)
		}
	}
	if len(addrs) == 0 {
		return nil, &PolicyError{Reason: fmt.Sprintf("destination %s resolved no addresses", host)}
	}

	allowPrivate, _ := ctx.Value(privateNetworkContextKey{}).(bool)
	validated := make([]netip.Addr, 0, len(addrs))
	for _, addr := range addrs {
		addr = addr.Unmap()
		if !addr.IsValid() || (!allowPrivate && !isPublicAddress(addr)) {
			return nil, &PolicyError{Reason: fmt.Sprintf("destination %s resolved non-public address %s", host, addr)}
		}
		validated = append(validated, addr)
	}

	var dialErr error
	for _, addr := range validated {
		conn, err := p.dialContext(ctx, network, net.JoinHostPort(addr.String(), port))
		if err == nil {
			return conn, nil
		}
		dialErr = err
	}
	return nil, fmt.Errorf("dial validated destination %s: %w", host, dialErr)
}

var nonPublicPrefixes = []netip.Prefix{
	netip.MustParsePrefix("0.0.0.0/8"),
	netip.MustParsePrefix("100.64.0.0/10"),
	netip.MustParsePrefix("192.0.0.0/24"),
	netip.MustParsePrefix("192.0.2.0/24"),
	netip.MustParsePrefix("198.18.0.0/15"),
	netip.MustParsePrefix("198.51.100.0/24"),
	netip.MustParsePrefix("203.0.113.0/24"),
	netip.MustParsePrefix("240.0.0.0/4"),
	netip.MustParsePrefix("fec0::/10"),
	netip.MustParsePrefix("2001:db8::/32"),
}

func isPublicAddress(addr netip.Addr) bool {
	if !addr.IsValid() || !addr.IsGlobalUnicast() || addr.IsPrivate() || addr.IsLoopback() || addr.IsLinkLocalUnicast() || addr.IsLinkLocalMulticast() || addr.IsMulticast() || addr.IsUnspecified() {
		return false
	}
	for _, prefix := range nonPublicPrefixes {
		if prefix.Contains(addr) {
			return false
		}
	}
	return true
}
