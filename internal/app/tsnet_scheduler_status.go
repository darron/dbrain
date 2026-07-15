package app

import (
	"context"
	"crypto/tls"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/darron/dbrain/internal/remote"
	"github.com/darron/dbrain/internal/schedulerstate"
)

type tsnetSchedulerStatusResponse struct {
	SyncAll schedulerstate.SyncAllStatus `json:"sync_all"`
}

type tsnetSchedulerStatusDiagnostic struct {
	Code       string `json:"code"`
	StatusCode int    `json:"status_code"`
}

type tsnetSchedulerHTTPStatusError struct {
	StatusCode int
}

func (e tsnetSchedulerHTTPStatusError) Error() string {
	return fmt.Sprintf("scheduler status request failed with HTTP status %d", e.StatusCode)
}

func schedulerStatusURL(webURL string) string {
	parsed, err := url.Parse(webURL)
	if err != nil {
		return ""
	}
	parsed.Path = "/api/scheduler/sync-all"
	parsed.RawQuery = ""
	parsed.Fragment = ""
	return parsed.String()
}

func fetchTSNetSchedulerStatus(ctx context.Context, rawURL string, tlsServerName string) (schedulerstate.SyncAllStatus, error) {
	probeCtx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(probeCtx, http.MethodGet, rawURL, nil)
	if err != nil {
		return schedulerstate.SyncAllStatus{}, err
	}
	transport := cloneDefaultHTTPTransport()
	if strings.TrimSpace(tlsServerName) != "" {
		transport.TLSClientConfig = &tls.Config{MinVersion: tls.VersionTLS12, ServerName: tlsServerName}
	}
	resp, err := (&http.Client{Timeout: 3 * time.Second, Transport: transport}).Do(req)
	if err != nil {
		return schedulerstate.SyncAllStatus{}, err
	}
	defer func() {
		_, _ = io.Copy(io.Discard, resp.Body)
		_ = resp.Body.Close()
	}()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return schedulerstate.SyncAllStatus{}, tsnetSchedulerHTTPStatusError{StatusCode: resp.StatusCode}
	}
	var payload tsnetSchedulerStatusResponse
	if err := json.NewDecoder(resp.Body).Decode(&payload); err != nil {
		return schedulerstate.SyncAllStatus{}, err
	}
	return payload.SyncAll, nil
}

func applyTSNetSchedulerStatus(ctx context.Context, opts remote.Options, info *tsnetStateInfo, certHost string, ipFallbacks []string, deps tsnetStatusDeps) {
	if deps.fetchScheduler == nil || info.WebURL == "" || !info.WebReachable {
		return
	}
	rawURL := schedulerStatusURL(info.WebURL)
	if rawURL == "" {
		return
	}
	status, err := fetchTSNetSchedulerStatusWithFallbacks(ctx, opts, rawURL, certHost, ipFallbacks, deps)
	if err != nil {
		var statusErr tsnetSchedulerHTTPStatusError
		if errors.As(err, &statusErr) && (statusErr.StatusCode == http.StatusUnauthorized || statusErr.StatusCode == http.StatusForbidden) {
			info.SyncAllError = &tsnetSchedulerStatusDiagnostic{Code: "scheduler_auth_failed", StatusCode: statusErr.StatusCode}
		}
		return
	}
	info.SyncAll = &status
}

func fetchTSNetSchedulerStatusWithFallbacks(ctx context.Context, opts remote.Options, rawURL string, certHost string, ipFallbacks []string, deps tsnetStatusDeps) (schedulerstate.SyncAllStatus, error) {
	status, err := deps.fetchScheduler(ctx, rawURL, "")
	if err == nil {
		return status, nil
	}
	firstErr := err
	if opts.TLS && certHost != "" && certHost != opts.Hostname {
		alternateURL := replaceURLHost(rawURL, opts.Hostname)
		if alternateURL != rawURL {
			status, err := deps.fetchScheduler(ctx, alternateURL, certHost)
			if err == nil {
				return status, nil
			}
		}
	}
	for _, ip := range ipFallbacks {
		ipURL := replaceURLHost(rawURL, ip)
		if ipURL == rawURL {
			continue
		}
		serverName := certHost
		if serverName == "" {
			serverName = opts.Hostname
		}
		status, err := deps.fetchScheduler(ctx, ipURL, serverName)
		if err == nil {
			return status, nil
		}
	}
	return schedulerstate.SyncAllStatus{}, firstErr
}
