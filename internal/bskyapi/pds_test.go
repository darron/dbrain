package bskyapi

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
)

func TestPDSResolverCachesDIDDocumentAndBuildsGetBlobURL(t *testing.T) {
	var didRequests int
	var server *httptest.Server
	server = httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		didRequests++
		if r.URL.Path != "/did:plc:one" {
			t.Fatalf("DID document path = %q", r.URL.Path)
		}
		_, _ = w.Write([]byte(`{"service":[{"id":"#atproto_pds","type":"AtprotoPersonalDataServer","serviceEndpoint":"` + server.URL + `"}]}`))
	}))
	defer server.Close()

	resolver := newPDSResolver(server.Client())
	resolver.documentURL = func(string) (string, error) { return server.URL + "/did:plc:one", nil }
	first, err := resolver.ResolveVideoBlob(context.Background(), "did:plc:one", "bafy-video")
	if err != nil {
		t.Fatalf("first ResolveVideoBlob: %v", err)
	}
	second, err := resolver.ResolveVideoBlob(context.Background(), "did:plc:one", "bafy-video-2")
	if err != nil {
		t.Fatalf("second ResolveVideoBlob: %v", err)
	}
	if didRequests != 1 {
		t.Fatalf("DID document requests = %d, want one cached request", didRequests)
	}
	for got, cid := range map[string]string{first: "bafy-video", second: "bafy-video-2"} {
		parsed, err := url.Parse(got)
		if err != nil {
			t.Fatalf("parse blob URL %q: %v", got, err)
		}
		if parsed.Path != "/xrpc/com.atproto.sync.getBlob" || parsed.Query().Get("did") != "did:plc:one" || parsed.Query().Get("cid") != cid {
			t.Fatalf("blob URL = %q", got)
		}
		if !strings.HasPrefix(got, server.URL+"/") {
			t.Fatalf("blob URL origin = %q, want %q", got, server.URL)
		}
	}
}

func TestPDSResolverRejectsMissingAtprotoPDSService(t *testing.T) {
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"service":[{"id":"#other","type":"Other","serviceEndpoint":"https://pds.example"}]}`))
	}))
	defer server.Close()

	resolver := newPDSResolver(server.Client())
	resolver.documentURL = func(string) (string, error) { return server.URL + "/did:plc:one", nil }
	_, err := resolver.ResolveVideoBlob(context.Background(), "did:plc:one", "bafy-video")
	if err == nil || !strings.Contains(err.Error(), "atproto_pds") {
		t.Fatalf("error = %v, want missing atproto_pds service", err)
	}
}

func TestPDSResolverRejectsPublicHTTPServiceEndpoint(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"service":[{"id":"#atproto_pds","type":"AtprotoPersonalDataServer","serviceEndpoint":"http://pds.example"}]}`))
	}))
	defer server.Close()

	resolver := newPDSResolver(server.Client())
	resolver.documentURL = func(string) (string, error) { return server.URL + "/did:plc:one", nil }
	_, err := resolver.ResolveVideoBlob(context.Background(), "did:plc:one", "bafy-video")
	if err == nil || !strings.Contains(err.Error(), "must use HTTPS") {
		t.Fatalf("error = %v, want HTTPS endpoint rejection", err)
	}
}
