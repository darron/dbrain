package app

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

func probeTSNetEndpoint(ctx context.Context, rawURL string, tlsServerName string) tsnetEndpointProbe {
	probeCtx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(probeCtx, http.MethodGet, rawURL, nil)
	if err != nil {
		return tsnetEndpointProbe{Error: err.Error(), CertHealth: "unknown"}
	}
	transport := cloneDefaultHTTPTransport()
	if strings.TrimSpace(tlsServerName) != "" {
		transport.TLSClientConfig = &tls.Config{MinVersion: tls.VersionTLS12, ServerName: tlsServerName}
	}
	resp, err := (&http.Client{Timeout: 3 * time.Second, Transport: transport}).Do(req)
	if err != nil {
		probe := tsnetEndpointProbe{Error: err.Error(), CertHealth: "unknown"}
		if dnsNames := certDNSNamesFromError(err); len(dnsNames) > 0 {
			probe.CertDNSNames = dnsNames
			probe.CertHealth = "error"
			probe.CertError = err.Error()
		}
		return probe
	}
	defer func() {
		_, _ = io.Copy(io.Discard, resp.Body)
		_ = resp.Body.Close()
	}()
	certHealth, certError := responseCertHealth(resp)
	return tsnetEndpointProbe{
		Reachable:    resp.StatusCode < 500,
		StatusCode:   resp.StatusCode,
		EffectiveURL: rawURL,
		CertHealth:   certHealth,
		CertError:    certError,
	}
}

func cloneDefaultHTTPTransport() *http.Transport {
	base, ok := http.DefaultTransport.(*http.Transport)
	if !ok {
		return &http.Transport{}
	}
	return base.Clone()
}

func certDNSNamesFromError(err error) []string {
	var hostnameErr x509.HostnameError
	if errors.As(err, &hostnameErr) && hostnameErr.Certificate != nil {
		return hostnameErr.Certificate.DNSNames
	}
	return nil
}

func responseCertHealth(resp *http.Response) (string, string) {
	if resp == nil || resp.TLS == nil || len(resp.TLS.PeerCertificates) == 0 {
		return "unknown", ""
	}
	cert := resp.TLS.PeerCertificates[0]
	now := time.Now()
	if now.Before(cert.NotBefore) {
		return "not_yet_valid", fmt.Sprintf("certificate not valid before %s", cert.NotBefore.Format(time.RFC3339))
	}
	if now.After(cert.NotAfter) {
		return "expired", fmt.Sprintf("certificate expired at %s", cert.NotAfter.Format(time.RFC3339))
	}
	return "ok", ""
}
