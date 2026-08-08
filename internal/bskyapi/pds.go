package bskyapi

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/darron/dbrain/internal/safehttp"
)

type videoBlobResolver interface {
	ResolveVideoBlob(context.Context, string, string) (string, error)
}

type pdsResolver struct {
	client      *http.Client
	cache       map[string]string
	documentURL func(string) (string, error)
}

func newPDSResolver(client *http.Client) *pdsResolver {
	if client == nil {
		client = safehttp.NewClient(safehttp.Policy{Timeout: 30 * time.Second})
	}
	return &pdsResolver{client: client, cache: make(map[string]string), documentURL: didDocumentURL}
}

func (r *pdsResolver) ResolveVideoBlob(ctx context.Context, did, cid string) (string, error) {
	did = strings.TrimSpace(did)
	cid = strings.TrimSpace(cid)
	if did == "" || cid == "" {
		return "", errors.New("video blob resolution requires DID and CID")
	}
	pds, ok := r.cache[did]
	if !ok {
		var err error
		pds, err = r.resolvePDS(ctx, did)
		if err != nil {
			return "", err
		}
		r.cache[did] = pds
	}
	endpoint, err := url.Parse(strings.TrimRight(pds, "/") + "/xrpc/com.atproto.sync.getBlob")
	if err != nil {
		return "", fmt.Errorf("build getBlob URL for %s: %w", did, err)
	}
	query := endpoint.Query()
	query.Set("did", did)
	query.Set("cid", cid)
	endpoint.RawQuery = query.Encode()
	return endpoint.String(), nil
}

func (r *pdsResolver) resolvePDS(ctx context.Context, did string) (string, error) {
	docURL, err := r.documentURL(did)
	if err != nil {
		return "", err
	}
	document, err := r.fetchJSON(ctx, docURL)
	if err != nil {
		return "", fmt.Errorf("resolve PDS for %s: %w", did, err)
	}
	services, ok := document["service"].([]any)
	if !ok {
		return "", fmt.Errorf("resolve PDS for %s: DID document has no service array", did)
	}
	for _, raw := range services {
		service, ok := raw.(map[string]any)
		if !ok {
			continue
		}
		id, _ := service["id"].(string)
		if !strings.HasSuffix(strings.TrimSpace(id), "#atproto_pds") {
			continue
		}
		endpoint := serviceEndpointString(service["serviceEndpoint"])
		canonical, err := safehttp.CanonicalOriginEndpoint(endpoint)
		if err != nil {
			return "", fmt.Errorf("resolve PDS for %s: invalid atproto_pds endpoint: %w", did, err)
		}
		parsed, _ := url.Parse(canonical)
		if parsed.Scheme != "https" {
			return "", fmt.Errorf("resolve PDS for %s: atproto_pds endpoint must use HTTPS", did)
		}
		return canonical, nil
	}
	return "", fmt.Errorf("resolve PDS for %s: no #atproto_pds service", did)
}

func didDocumentURL(did string) (string, error) {
	if !strings.HasPrefix(did, "did:") {
		return "", fmt.Errorf("invalid DID %q", did)
	}
	switch {
	case strings.HasPrefix(did, "did:plc:"):
		return "https://plc.directory/" + url.PathEscape(did), nil
	case strings.HasPrefix(did, "did:web:"):
		parts := strings.Split(strings.TrimPrefix(did, "did:web:"), ":")
		if len(parts) == 0 || strings.TrimSpace(parts[0]) == "" {
			return "", fmt.Errorf("invalid did:web %q", did)
		}
		host, err := url.PathUnescape(parts[0])
		if err != nil {
			return "", fmt.Errorf("decode did:web host %q: %w", did, err)
		}
		path := "/.well-known/did.json"
		if len(parts) > 1 {
			path = "/" + strings.Join(parts[1:], "/") + "/did.json"
		}
		return (&url.URL{Scheme: "https", Host: host, Path: path}).String(), nil
	default:
		return "", fmt.Errorf("unsupported DID method in %q", did)
	}
}

func (r *pdsResolver) fetchJSON(ctx context.Context, rawURL string) (map[string]any, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
	if err != nil {
		return nil, fmt.Errorf("build DID document request: %w", err)
	}
	req.Header.Set("Accept", "application/did+json, application/json")
	resp, err := r.client.Do(req)
	if err != nil {
		return nil, err
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("DID document returned HTTP %d", resp.StatusCode)
	}
	var document map[string]any
	if err := json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(&document); err != nil {
		return nil, fmt.Errorf("decode DID document: %w", err)
	}
	return document, nil
}

func serviceEndpointString(value any) string {
	if endpoint, ok := value.(string); ok {
		return strings.TrimSpace(endpoint)
	}
	object, ok := value.(map[string]any)
	if !ok {
		return ""
	}
	if endpoint, ok := object["uri"].(string); ok {
		return strings.TrimSpace(endpoint)
	}
	return ""
}
