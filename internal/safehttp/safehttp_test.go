package safehttp

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestClientAppliesDeepTransportPhaseTimeouts(t *testing.T) {
	client := NewClient(Policy{ConnectTimeout: 10 * time.Second, TLSHandshakeTimeout: 11 * time.Second, ResponseHeaderTimeout: 12 * time.Second})
	transport, ok := client.Transport.(*policyTransport)
	if !ok {
		t.Fatalf("transport = %T", client.Transport)
	}
	if transport.base.TLSHandshakeTimeout != 11*time.Second || transport.base.ResponseHeaderTimeout != 12*time.Second {
		t.Fatalf("transport timeouts = tls:%s header:%s", transport.base.TLSHandshakeTimeout, transport.base.ResponseHeaderTimeout)
	}
}

func TestClientRejectsNonPublicDestinationsBeforeDial(t *testing.T) {
	tests := []struct {
		name string
		ips  []netip.Addr
	}{
		{name: "loopback IPv4", ips: testAddrs("127.0.0.1")},
		{name: "loopback IPv6", ips: testAddrs("::1")},
		{name: "private IPv4", ips: testAddrs("10.0.0.1")},
		{name: "private IPv6", ips: testAddrs("fd00::1")},
		{name: "site-local IPv6", ips: testAddrs("fec0::1")},
		{name: "mapped private IPv4", ips: testAddrs("::ffff:192.168.1.1")},
		{name: "CGNAT and Tailscale", ips: testAddrs("100.100.100.100")},
		{name: "link local metadata", ips: testAddrs("169.254.169.254")},
		{name: "mixed public and private DNS", ips: testAddrs("8.8.8.8", "127.0.0.1")},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			dials := 0
			client := NewClient(Policy{
				LookupNetIP: staticResolver(test.ips),
				DialContext: func(context.Context, string, string) (net.Conn, error) {
					dials++
					return nil, errors.New("unexpected dial")
				},
			})

			_, err := client.Get("http://destination.test/resource")
			if !IsPolicyError(err) {
				t.Fatalf("error = %v, want policy rejection", err)
			}
			if dials != 0 {
				t.Fatalf("dial calls = %d, want 0", dials)
			}
		})
	}
}

func TestClientRejectsInvalidImportedURLsBeforeDial(t *testing.T) {
	for _, rawURL := range []string{
		"ftp://destination.test/resource",
		"http://user:secret@destination.test/resource",
	} {
		t.Run(rawURL, func(t *testing.T) {
			dials := 0
			client := NewClient(Policy{
				LookupNetIP: staticResolver(testAddrs("8.8.8.8")),
				DialContext: func(context.Context, string, string) (net.Conn, error) {
					dials++
					return nil, errors.New("unexpected dial")
				},
			})

			_, err := client.Get(rawURL)
			if !IsPolicyError(err) {
				t.Fatalf("error = %v, want policy rejection", err)
			}
			if dials != 0 {
				t.Fatalf("dial calls = %d, want 0", dials)
			}
		})
	}
}

func TestClientRejectsRedirectToPrivateDestination(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Host == "public.test" {
			http.Redirect(w, r, "http://private.test/secret", http.StatusFound)
			return
		}
		_, _ = w.Write([]byte("private response"))
	}))
	defer server.Close()

	dials := 0
	client := NewClient(Policy{
		LookupNetIP: mapResolver(map[string][]netip.Addr{
			"public.test":  testAddrs("8.8.8.8"),
			"private.test": testAddrs("127.0.0.1"),
		}),
		DialContext: dialTestServer(server.Listener.Addr().String(), &dials),
	})

	_, err := client.Get("http://public.test/start")
	if !IsPolicyError(err) {
		t.Fatalf("error = %v, want redirect policy rejection", err)
	}
	if dials != 1 {
		t.Fatalf("dial calls = %d, want only initial public dial", dials)
	}
}

func TestClientAllowsExactPrivateOriginAndSameOriginRedirect(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/start" {
			http.Redirect(w, r, "/final", http.StatusFound)
			return
		}
		_, _ = w.Write([]byte("configured reader"))
	}))
	defer server.Close()

	client := NewClient(Policy{
		AllowedPrivateOrigins: []string{"http://reader.test"},
		LookupNetIP:           staticResolver(testAddrs("127.0.0.1")),
		DialContext:           dialTestServer(server.Listener.Addr().String(), nil),
	})

	resp, err := client.Get("http://reader.test/start")
	if err != nil {
		t.Fatalf("configured origin request: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.Request.URL.String() != "http://reader.test/final" {
		t.Fatalf("final URL = %q", resp.Request.URL.String())
	}
}

func TestClientDoesNotLeakPrivateOriginAllowanceToFallbackHost(t *testing.T) {
	client := NewClient(Policy{
		AllowedPrivateOrigins: []string{"http://reader.test"},
		LookupNetIP:           staticResolver(testAddrs("127.0.0.1")),
		DialContext: func(context.Context, string, string) (net.Conn, error) {
			return nil, errors.New("unexpected dial")
		},
	})

	_, err := client.Get("http://fallback.test/resource")
	if !IsPolicyError(err) {
		t.Fatalf("error = %v, want private fallback rejection", err)
	}
}

func TestClientEnforcesRedirectLimit(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, fmt.Sprintf("/redirect-%s", strings.TrimPrefix(r.URL.Path, "/redirect-")), http.StatusFound)
	}))
	defer server.Close()

	client := NewClient(Policy{
		MaxRedirects: 2,
		LookupNetIP:  staticResolver(testAddrs("8.8.8.8")),
		DialContext:  dialTestServer(server.Listener.Addr().String(), nil),
	})
	_, err := client.Get("http://public.test/redirect-start")
	if !IsPolicyError(err) || !strings.Contains(err.Error(), "stopped after 2 redirects") {
		t.Fatalf("error = %v, want explicit redirect limit", err)
	}
}

func TestClientDisablesEnvironmentProxy(t *testing.T) {
	t.Setenv("HTTP_PROXY", "http://127.0.0.1:1")
	t.Setenv("NO_PROXY", "")

	var dialed string
	client := NewClient(Policy{
		LookupNetIP: staticResolver(testAddrs("8.8.8.8")),
		DialContext: func(_ context.Context, _ string, address string) (net.Conn, error) {
			dialed = address
			return nil, errors.New("dial marker")
		},
	})
	_, _ = client.Get("http://public.test/resource")
	if dialed != "8.8.8.8:80" {
		t.Fatalf("dialed %q, want validated destination instead of environment proxy", dialed)
	}
}

func TestClientDialsValidatedNumericIPAndPreservesHostAndTLSServerName(t *testing.T) {
	var mu sync.Mutex
	var serverName string
	var requestHost string
	server := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requestHost = r.Host
		_, _ = w.Write([]byte("ok"))
	}))
	server.TLS = &tls.Config{
		GetConfigForClient: func(info *tls.ClientHelloInfo) (*tls.Config, error) {
			mu.Lock()
			serverName = info.ServerName
			mu.Unlock()
			return nil, nil
		},
	}
	server.StartTLS()
	defer server.Close()

	serverTransport := server.Client().Transport.(*http.Transport)
	var dialed string
	client := NewClient(Policy{
		TLSClientConfig: serverTransport.TLSClientConfig,
		LookupNetIP:     staticResolver(testAddrs("8.8.8.8")),
		DialContext: func(ctx context.Context, network, address string) (net.Conn, error) {
			dialed = address
			var dialer net.Dialer
			return dialer.DialContext(ctx, network, server.Listener.Addr().String())
		},
	})

	resp, err := client.Get("https://example.com/resource")
	if err != nil {
		t.Fatalf("TLS request: %v", err)
	}
	_ = resp.Body.Close()
	mu.Lock()
	gotServerName := serverName
	mu.Unlock()
	if dialed != "8.8.8.8:443" {
		t.Fatalf("dialed %q, want numeric validated IP", dialed)
	}
	if requestHost != "example.com" {
		t.Fatalf("request Host = %q, want example.com", requestHost)
	}
	if gotServerName != "example.com" {
		t.Fatalf("TLS ServerName = %q, want example.com", gotServerName)
	}
}

func TestClientAllowsPrivateNetworkOnlyWhenExplicit(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte("local control"))
	}))
	defer server.Close()

	client := NewClient(Policy{AllowPrivateNetwork: true})
	resp, err := client.Get(server.URL)
	if err != nil {
		t.Fatalf("explicit private-network request: %v", err)
	}
	_ = resp.Body.Close()
}

func TestCanonicalOriginEndpointRejectsNonOriginComponents(t *testing.T) {
	for _, raw := range []string{
		"https://user:secret@s3.example.com",
		"https://s3.example.com/bucket",
		"https://s3.example.com?region=auto",
		"https://s3.example.com#fragment",
		"ftp://s3.example.com",
	} {
		t.Run(raw, func(t *testing.T) {
			if _, err := CanonicalOriginEndpoint(raw); !IsPolicyError(err) {
				t.Fatalf("error = %v, want policy error", err)
			}
		})
	}
	got, err := CanonicalOriginEndpoint("HTTPS://S3.Example.COM.:443/")
	if err != nil {
		t.Fatal(err)
	}
	if got != "https://s3.example.com:443" {
		t.Fatalf("canonical origin = %q", got)
	}
}

func TestClientRestrictsRequestsAndRedirectsToExactConfiguredOrigin(t *testing.T) {
	dials := 0
	client := NewClient(Policy{
		AllowedOrigins: []string{"https://s3.example.com"},
		LookupNetIP:    staticResolver(testAddrs("8.8.8.8")),
		DialContext: func(context.Context, string, string) (net.Conn, error) {
			dials++
			return nil, errors.New("unexpected dial")
		},
	})
	_, err := client.Get("https://fallback.example.com/object")
	if !IsPolicyError(err) || dials != 0 {
		t.Fatalf("error=%v dials=%d, want exact-origin rejection before dial", err, dials)
	}
}

func staticResolver(addrs []netip.Addr) func(context.Context, string, string) ([]netip.Addr, error) {
	return func(context.Context, string, string) ([]netip.Addr, error) {
		return append([]netip.Addr(nil), addrs...), nil
	}
}

func mapResolver(addresses map[string][]netip.Addr) func(context.Context, string, string) ([]netip.Addr, error) {
	return func(_ context.Context, _ string, host string) ([]netip.Addr, error) {
		addrs, ok := addresses[host]
		if !ok {
			return nil, fmt.Errorf("unexpected host %q", host)
		}
		return append([]netip.Addr(nil), addrs...), nil
	}
}

func dialTestServer(serverAddress string, calls *int) func(context.Context, string, string) (net.Conn, error) {
	return func(ctx context.Context, network, _ string) (net.Conn, error) {
		if calls != nil {
			*calls++
		}
		var dialer net.Dialer
		return dialer.DialContext(ctx, network, serverAddress)
	}
}

func testAddrs(values ...string) []netip.Addr {
	addrs := make([]netip.Addr, 0, len(values))
	for _, value := range values {
		addrs = append(addrs, netip.MustParseAddr(value))
	}
	return addrs
}
