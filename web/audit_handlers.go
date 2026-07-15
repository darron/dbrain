package web

import (
	"encoding/json"
	"errors"
	"io"
	"mime"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/darron/dbrain/internal/audit"
)

const (
	maxAuditRunBodyBytes   = 4 << 10
	maxAuditReadQueryBytes = 128
)

type auditRunRequest struct {
	Profile audit.Profile `json:"profile"`
}

func (s *server) handleAuditLatest(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeMethodNotAllowed(w, http.MethodGet)
		return
	}
	query, ok := parseAuditReadQuery(w, r.URL.RawQuery, r.URL.ForceQuery, false)
	if !ok {
		return
	}
	if s.auditReports == nil {
		writeMessage(w, http.StatusServiceUnavailable, "audit_report_unavailable")
		return
	}
	report, err := s.auditReports.Latest(query.profile)
	if err != nil {
		writeMessage(w, http.StatusServiceUnavailable, "audit_report_unavailable")
		return
	}
	if report != nil && !validWebAuditReport(*report, query.profile) {
		writeMessage(w, http.StatusServiceUnavailable, "audit_report_unavailable")
		return
	}
	writeJSON(w, http.StatusOK, audit.PresentReport(report, query.profile, s.auditSyncInterval, s.auditStandardInterval, s.auditTime()))
}

func (s *server) handleAuditHistory(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeMethodNotAllowed(w, http.MethodGet)
		return
	}
	query, ok := parseAuditReadQuery(w, r.URL.RawQuery, r.URL.ForceQuery, true)
	if !ok {
		return
	}
	if s.auditReports == nil {
		writeMessage(w, http.StatusServiceUnavailable, "audit_report_unavailable")
		return
	}
	reports, err := s.auditReports.History(query.profile, query.limit)
	if err != nil {
		writeMessage(w, http.StatusServiceUnavailable, "audit_report_unavailable")
		return
	}
	if len(reports) > query.limit {
		reports = reports[:query.limit]
	}
	history := make([]AuditHistoryEntry, 0, len(reports))
	now := s.auditTime()
	for _, report := range reports {
		if !validWebAuditReport(report, query.profile) {
			writeMessage(w, http.StatusServiceUnavailable, "audit_report_unavailable")
			return
		}
		presented := audit.PresentReport(&report, query.profile, s.auditSyncInterval, s.auditStandardInterval, now)
		history = append(history, AuditHistoryEntry{
			AuditID: report.AuditID, Profile: report.Profile, Status: report.Status, Confidence: report.Confidence,
			StartedAt: report.StartedAt, CompletedAt: report.CompletedAt, Summary: report.Summary, Freshness: presented.Freshness,
		})
	}
	writeJSON(w, http.StatusOK, AuditHistoryResponse{Profile: query.profile, History: history})
}

func (s *server) handleAuditRun(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeMethodNotAllowed(w, http.MethodPost)
		return
	}
	if r.URL.RawQuery != "" || r.URL.ForceQuery {
		writeMessage(w, http.StatusBadRequest, "audit run does not accept query parameters")
		return
	}
	mediaType, _, err := mime.ParseMediaType(r.Header.Get("Content-Type"))
	if err != nil || mediaType != "application/json" {
		writeMessage(w, http.StatusUnsupportedMediaType, "content type must be application/json")
		return
	}
	limited := http.MaxBytesReader(w, r.Body, maxAuditRunBodyBytes)
	defer func() { _ = limited.Close() }()
	decoder := json.NewDecoder(limited)
	decoder.DisallowUnknownFields()
	var request auditRunRequest
	if err := decoder.Decode(&request); err != nil {
		var tooLarge *http.MaxBytesError
		if errors.As(err, &tooLarge) {
			writeMessage(w, http.StatusRequestEntityTooLarge, "audit request body too large")
			return
		}
		writeMessage(w, http.StatusBadRequest, "invalid audit run request")
		return
	}
	if err := ensureAuditRequestEOF(decoder); err != nil {
		var tooLarge *http.MaxBytesError
		if errors.As(err, &tooLarge) {
			writeMessage(w, http.StatusRequestEntityTooLarge, "audit request body too large")
			return
		}
		writeMessage(w, http.StatusBadRequest, "invalid audit run request")
		return
	}
	if request.Profile != audit.ProfileFast && request.Profile != audit.ProfileStandard {
		writeMessage(w, http.StatusBadRequest, "audit profile must be fast or standard")
		return
	}
	if s.auditRuns == nil {
		writeMessage(w, http.StatusServiceUnavailable, "audit_run_unavailable")
		return
	}
	result := s.auditRuns.Start(request.Profile)
	switch result.Kind {
	case AuditRunStarted, AuditRunDeduplicated:
		writeJSON(w, http.StatusAccepted, result.Status)
	case AuditRunConflict:
		writeJSON(w, http.StatusConflict, AuditRunConflictResponse{Error: "audit_run_conflict", ActiveAuditID: result.ActiveAuditID, ActiveProfile: result.ActiveProfile})
	case AuditRunRateLimited:
		writeJSON(w, http.StatusTooManyRequests, AuditRunRateLimitResponse{Error: "audit_run_rate_limited", RetryAfterSeconds: result.RetryAfterSeconds})
	default:
		writeMessage(w, http.StatusServiceUnavailable, "audit_run_unavailable")
	}
}

func (s *server) handleAuditRunStatus(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeMethodNotAllowed(w, http.MethodGet)
		return
	}
	if r.URL.RawQuery != "" || r.URL.ForceQuery {
		writeMessage(w, http.StatusBadRequest, "audit run status does not accept query parameters")
		return
	}
	id := strings.TrimPrefix(r.URL.Path, "/api/audit/runs/")
	if id == "" || strings.Contains(id, "/") || len(id) > 128 || s.auditRuns == nil {
		http.NotFound(w, r)
		return
	}
	status, ok := s.auditRuns.Status(id)
	if !ok {
		http.NotFound(w, r)
		return
	}
	writeJSON(w, http.StatusOK, status)
}

type auditReadQuery struct {
	profile audit.Profile
	limit   int
}

func parseAuditReadQuery(w http.ResponseWriter, rawQuery string, forceQuery, history bool) (auditReadQuery, bool) {
	query := auditReadQuery{profile: audit.ProfileStandard, limit: 20}
	if forceQuery || len(rawQuery) > maxAuditReadQueryBytes {
		writeMessage(w, http.StatusBadRequest, "invalid audit query")
		return auditReadQuery{}, false
	}
	if rawQuery == "" {
		return query, true
	}

	seenProfile := false
	seenLimit := false
	for _, component := range strings.Split(rawQuery, "&") {
		if component == "" || strings.Count(component, "=") != 1 {
			writeMessage(w, http.StatusBadRequest, "invalid audit query")
			return auditReadQuery{}, false
		}
		key, value, _ := strings.Cut(component, "=")
		switch key {
		case "profile":
			if seenProfile || (value != string(audit.ProfileFast) && value != string(audit.ProfileStandard)) {
				writeMessage(w, http.StatusBadRequest, "invalid audit query")
				return auditReadQuery{}, false
			}
			seenProfile = true
			query.profile = audit.Profile(value)
		case "limit":
			if !history || seenLimit {
				writeMessage(w, http.StatusBadRequest, "invalid audit query")
				return auditReadQuery{}, false
			}
			limit, err := strconv.Atoi(value)
			if err != nil || limit < 1 || limit > 100 || strconv.Itoa(limit) != value {
				writeMessage(w, http.StatusBadRequest, "invalid audit query")
				return auditReadQuery{}, false
			}
			seenLimit = true
			query.limit = limit
		default:
			writeMessage(w, http.StatusBadRequest, "invalid audit query")
			return auditReadQuery{}, false
		}
	}
	return query, true
}

func ensureAuditRequestEOF(decoder *json.Decoder) error {
	var extra json.RawMessage
	if err := decoder.Decode(&extra); errors.Is(err, io.EOF) {
		return nil
	} else if err != nil {
		return err
	}
	return errors.New("trailing JSON value")
}

func validWebAuditReport(report audit.Report, profile audit.Profile) bool {
	return report.Profile == profile && report.Scope.WholeSystem && !report.Scope.Filtered && audit.ValidateReport(report) == nil
}

func (s *server) auditTime() time.Time {
	if s.auditNow != nil {
		return s.auditNow().UTC()
	}
	return time.Now().UTC()
}
