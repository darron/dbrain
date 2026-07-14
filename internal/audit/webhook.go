package audit

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"strings"
	"time"

	"github.com/darron/dbrain/internal/runtimeenv"
	"github.com/darron/dbrain/internal/safehttp"
)

const MaxWebhookBytes int64 = 64 << 10

type WebhookConfig struct {
	URL                string
	BearerTokenRef     string
	AllowPrivateOrigin bool
	AdminOrigin        string
}

type Webhook struct {
	configured    bool
	target        string
	tokenRef      string
	adminOrigin   string
	client        *http.Client
	resolveSecret func(context.Context, string) (string, error)
}

type webhookBuildIdentity struct {
	Version               string `json:"version"`
	Commit                string `json:"commit"`
	Platform              string `json:"platform"`
	SecurityBaseline      string `json:"security_baseline"`
	SecurityBaselineEpoch int    `json:"security_baseline_epoch"`
}

type webhookChange struct {
	CheckID CheckID `json:"check_id"`
	Summary string  `json:"summary"`
}

type webhookPayload struct {
	Build         webhookBuildIdentity `json:"build"`
	OverallStatus Status               `json:"overall_status"`
	Changes       []webhookChange      `json:"changes"`
	ObservedAt    time.Time            `json:"observed_at"`
	AdminOrigin   string               `json:"admin_origin,omitempty"`
}

func NewWebhook(cfg WebhookConfig) (*Webhook, error) {
	return newWebhookWithPolicy(cfg, safehttp.Policy{})
}

func newWebhookWithPolicy(cfg WebhookConfig, injected safehttp.Policy) (*Webhook, error) {
	raw := strings.TrimSpace(cfg.URL)
	if raw == "" {
		return &Webhook{resolveSecret: runtimeenv.ResolveSecretRef}, nil
	}
	if err := validateTypedWebhookSecretRef(cfg.BearerTokenRef); err != nil {
		return nil, err
	}
	target, origin, obviousPrivate, err := validateWebhookURL(raw, cfg.AllowPrivateOrigin)
	if err != nil {
		return nil, err
	}
	adminOrigin := ""
	if rawAdmin := strings.TrimSpace(cfg.AdminOrigin); rawAdmin != "" {
		adminOrigin, err = safehttp.CanonicalOriginEndpoint(rawAdmin)
		if err != nil {
			return nil, fmt.Errorf("invalid audit admin origin")
		}
	}
	policy := safehttp.Policy{
		Timeout:          10 * time.Second,
		ConnectTimeout:   10 * time.Second,
		DisableRedirects: true,
		AllowedOrigins:   []string{origin},
		LookupNetIP:      injected.LookupNetIP,
		DialContext:      injected.DialContext,
		TLSClientConfig:  injected.TLSClientConfig,
	}
	if obviousPrivate || (cfg.AllowPrivateOrigin && strings.HasPrefix(origin, "https://")) {
		policy.AllowedPrivateOrigins = []string{origin}
	}
	return &Webhook{
		configured: true, target: target, tokenRef: strings.TrimSpace(cfg.BearerTokenRef), adminOrigin: adminOrigin,
		client: safehttp.NewClient(policy), resolveSecret: runtimeenv.ResolveSecretRef,
	}, nil
}

func validateTypedWebhookSecretRef(raw string) error {
	value := strings.TrimSpace(raw)
	if value == "" {
		return nil
	}
	switch {
	case strings.HasPrefix(value, "env:"):
		if strings.TrimSpace(strings.TrimPrefix(value, "env:")) != "" {
			return nil
		}
	case strings.HasPrefix(value, "op://"):
		if strings.TrimSpace(strings.TrimPrefix(value, "op://")) != "" {
			return nil
		}
	case strings.HasPrefix(value, "keychain://"):
		parts := strings.SplitN(strings.TrimPrefix(value, "keychain://"), "/", 2)
		if len(parts) == 2 && strings.TrimSpace(parts[0]) != "" && strings.TrimSpace(parts[1]) != "" {
			return nil
		}
	}
	return fmt.Errorf("audit webhook bearer token must be a typed secret ref")
}

func (w *Webhook) Configured() bool { return w != nil && w.configured }

func validateWebhookURL(raw string, allowPrivate bool) (target, origin string, obviousPrivate bool, err error) {
	parsed, parseErr := url.Parse(raw)
	if parseErr != nil || parsed.Opaque != "" || parsed.User != nil || parsed.Hostname() == "" {
		return "", "", false, fmt.Errorf("invalid audit webhook URL")
	}
	scheme := strings.ToLower(parsed.Scheme)
	if scheme != "http" && scheme != "https" {
		return "", "", false, fmt.Errorf("invalid audit webhook URL scheme")
	}
	if parsed.RawQuery != "" || parsed.ForceQuery || parsed.Fragment != "" {
		return "", "", false, fmt.Errorf("audit webhook URL query and fragment are not allowed")
	}
	if parsed.RawPath != "" {
		if _, unescapeErr := url.PathUnescape(parsed.RawPath); unescapeErr != nil {
			return "", "", false, fmt.Errorf("invalid audit webhook path")
		}
	}
	origin, err = safehttp.CanonicalOriginEndpoint(scheme + "://" + parsed.Host)
	if err != nil {
		return "", "", false, fmt.Errorf("invalid audit webhook origin")
	}
	obviousPrivate = isObviousPrivateWebhookHost(parsed.Hostname())
	if obviousPrivate && !allowPrivate {
		return "", "", false, fmt.Errorf("private audit webhook origin requires explicit opt-in")
	}
	if scheme != "https" && !obviousPrivate {
		return "", "", false, fmt.Errorf("public audit webhook origin requires HTTPS")
	}
	path := parsed.EscapedPath()
	if path == "" {
		path = "/"
	}
	return origin + path, origin, obviousPrivate, nil
}

func isObviousPrivateWebhookHost(host string) bool {
	host = strings.ToLower(strings.TrimSuffix(strings.TrimSpace(host), "."))
	if host == "localhost" || strings.HasSuffix(host, ".localhost") {
		return true
	}
	ip := net.ParseIP(host)
	if ip == nil {
		return false
	}
	addr, ok := netip.AddrFromSlice(ip)
	if !ok {
		return true
	}
	addr = addr.Unmap()
	if !addr.IsValid() || !addr.IsGlobalUnicast() || addr.IsPrivate() || addr.IsLoopback() || addr.IsLinkLocalUnicast() || addr.IsLinkLocalMulticast() || addr.IsMulticast() || addr.IsUnspecified() {
		return true
	}
	return netip.MustParsePrefix("100.64.0.0/10").Contains(addr)
}

func (w *Webhook) Deliver(ctx context.Context, report Report, decision AlertDecision) error {
	if w == nil || !w.configured || !decision.Notify {
		return nil
	}
	changes := make([]webhookChange, 0, len(decision.Changes))
	for _, change := range decision.Changes {
		if _, ok := Lookup(change.CheckID); !ok || !change.Status.Valid() || change.Status == StatusSkipped {
			return fmt.Errorf("invalid audit webhook change")
		}
		changes = append(changes, webhookChange{CheckID: change.CheckID, Summary: fixedSummary(change.CheckID)})
	}
	payload := webhookPayload{
		Build: webhookBuildIdentity{
			Version: report.Boundary.Version, Commit: report.Boundary.Commit, Platform: report.Boundary.Platform,
			SecurityBaseline: report.Boundary.SecurityBaseline, SecurityBaselineEpoch: report.Boundary.SecurityBaselineEpoch,
		},
		OverallStatus: report.Status, Changes: changes,
		ObservedAt: report.CompletedAt.UTC(), AdminOrigin: w.adminOrigin,
	}
	data, err := json.Marshal(payload)
	if err != nil || int64(len(data)) > MaxWebhookBytes {
		return fmt.Errorf("audit webhook request exceeds byte limit")
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, w.target, bytes.NewReader(data))
	if err != nil {
		return fmt.Errorf("create audit webhook request")
	}
	request.Header.Set("Content-Type", "application/json")
	token := ""
	if w.tokenRef != "" {
		resolved, resolveErr := w.resolveSecret(ctx, w.tokenRef)
		token = strings.TrimSpace(resolved)
		if resolveErr != nil || strings.TrimSpace(token) == "" {
			return fmt.Errorf("resolve audit webhook authentication")
		}
	}
	if webhookControlledRequestBytes(w.target, data, token) > MaxWebhookBytes {
		return fmt.Errorf("audit webhook request exceeds byte limit")
	}
	if token != "" {
		request.Header.Set("Authorization", "Bearer "+token)
	}
	response, err := w.client.Do(request)
	if err != nil {
		return fmt.Errorf("deliver audit webhook")
	}
	defer func() { _ = response.Body.Close() }()
	read, readErr := io.Copy(io.Discard, io.LimitReader(response.Body, MaxWebhookBytes+1))
	if readErr != nil {
		return fmt.Errorf("read audit webhook response")
	}
	if read > MaxWebhookBytes {
		return fmt.Errorf("audit webhook response exceeds byte limit")
	}
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return fmt.Errorf("audit webhook returned non-success status")
	}
	return nil
}

func webhookControlledRequestBytes(target string, body []byte, token string) int64 {
	// Include every caller-controlled wire component plus conservative space for
	// the fixed HTTP/1.1 framing and headers added by net/http. Counting the full
	// absolute target (rather than only RequestURI) intentionally overestimates.
	const fixed = "POST  HTTP/1.1\r\nHost: \r\nContent-Type: application/json\r\nAuthorization: Bearer \r\nContent-Length: 18446744073709551615\r\nUser-Agent: Go-http-client/1.1\r\nAccept-Encoding: gzip\r\n\r\n"
	parsed, _ := url.Parse(target)
	return int64(len(fixed) + len(target) + len(parsed.Host) + len(body) + len(token))
}
