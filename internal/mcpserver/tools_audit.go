package mcpserver

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"time"

	"golang.org/x/sync/singleflight"

	"github.com/darron/dbrain/internal/audit"
)

const (
	auditToolDeadline = 10 * time.Second
	auditToolMaxBytes = 256 << 10
)

var auditFastSingleflight singleflight.Group

var (
	errInvalidAuditArguments   = errors.New("invalid dbrain_audit arguments")
	errUnsupportedAuditProfile = errors.New("unsupported audit profile; use fast or standard")
	errFastAuditTimedOut       = errors.New("fast audit timed out")
)

type auditToolArguments struct {
	Profile audit.Profile `json:"profile"`
}

func (s *Server) callAudit(ctx context.Context, raw json.RawMessage) (map[string]interface{}, error) {
	arguments, err := decodeAuditToolArguments(raw)
	if err != nil {
		return nil, err
	}

	var presented audit.PresentedReport
	switch arguments.Profile {
	case audit.ProfileFast:
		presented, err = s.runFastAudit(ctx)
	case audit.ProfileStandard:
		presented, err = s.readStandardAudit()
	default:
		return nil, errUnsupportedAuditProfile
	}
	if err != nil {
		return nil, err
	}
	if err := validateAuditPresentation(presented, arguments.Profile); err != nil {
		return nil, fmt.Errorf("audit result is unavailable")
	}

	status := "unknown"
	checks := 0
	if presented.Report != nil {
		status = string(presented.Report.Status)
		checks = len(presented.Report.Checks)
	}
	result := toolOKResult(
		fmt.Sprintf("Audit profile=%s status=%s freshness=%s checks=%d", arguments.Profile, status, presented.Freshness.Status, checks),
		presented,
	)
	encoded, err := json.Marshal(result)
	if err != nil {
		return nil, fmt.Errorf("encode audit result")
	}
	if len(encoded) > auditToolMaxBytes {
		return nil, fmt.Errorf("audit result exceeds the 256 KiB MCP limit")
	}
	return result, nil
}

func decodeAuditToolArguments(raw json.RawMessage) (auditToolArguments, error) {
	arguments := auditToolArguments{Profile: audit.ProfileFast}
	trimmed := bytes.TrimSpace(raw)
	if len(trimmed) == 0 {
		return arguments, nil
	}
	if trimmed[0] != '{' {
		return auditToolArguments{}, errInvalidAuditArguments
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&arguments); err != nil {
		return auditToolArguments{}, errInvalidAuditArguments
	}
	if err := ensureAuditJSONEOF(decoder); err != nil {
		return auditToolArguments{}, err
	}
	if arguments.Profile == "" {
		arguments.Profile = audit.ProfileFast
	}
	if arguments.Profile != audit.ProfileFast && arguments.Profile != audit.ProfileStandard {
		return auditToolArguments{}, errUnsupportedAuditProfile
	}
	return arguments, nil
}

func ensureAuditJSONEOF(decoder *json.Decoder) error {
	var extra interface{}
	if err := decoder.Decode(&extra); errors.Is(err, io.EOF) {
		return nil
	} else if err != nil {
		return errInvalidAuditArguments
	}
	return errInvalidAuditArguments
}

func (s *Server) runFastAudit(ctx context.Context) (audit.PresentedReport, error) {
	if s.deps.Audit.RunFast == nil {
		return audit.PresentedReport{}, fmt.Errorf("fast audit is unavailable")
	}
	newTimer := s.newAuditTimer
	if newTimer == nil {
		newTimer = newWallClockAuditTimer
	}
	deadline := newTimer(auditToolDeadline)
	if deadline.stop != nil {
		defer deadline.stop()
	}
	result := auditFastSingleflight.DoChan("local-fast", func() (interface{}, error) {
		runCtx, cancel := context.WithTimeout(context.Background(), auditToolDeadline)
		defer cancel()
		report, err := s.deps.Audit.RunFast(runCtx)
		if err != nil {
			return nil, err
		}
		return report, nil
	})
	select {
	case <-ctx.Done():
		return audit.PresentedReport{}, fmt.Errorf("fast audit canceled")
	case <-deadline.done:
		return audit.PresentedReport{}, errFastAuditTimedOut
	case shared := <-result:
		if shared.Err != nil {
			return audit.PresentedReport{}, fmt.Errorf("fast audit failed")
		}
		report, ok := shared.Val.(audit.Report)
		if !ok {
			return audit.PresentedReport{}, fmt.Errorf("fast audit failed")
		}
		cloned, err := cloneAuditReport(report)
		if err != nil {
			return audit.PresentedReport{}, fmt.Errorf("fast audit result is invalid")
		}
		return audit.PresentReport(&cloned, audit.ProfileFast, s.deps.Audit.SyncInterval, s.deps.Audit.StandardInterval, cloned.CompletedAt), nil
	}
}

func (s *Server) readStandardAudit() (audit.PresentedReport, error) {
	if s.deps.Audit.Reports == nil {
		return audit.PresentedReport{}, fmt.Errorf("standard audit report store is unavailable")
	}
	report, err := s.deps.Audit.Reports.Latest(audit.ProfileStandard)
	if err != nil {
		return audit.PresentedReport{}, fmt.Errorf("standard audit report is unavailable")
	}
	if report == nil {
		return audit.PresentReport(nil, audit.ProfileStandard, s.deps.Audit.SyncInterval, s.deps.Audit.StandardInterval, s.auditNow()), nil
	}
	cloned, err := cloneAuditReport(*report)
	if err != nil {
		return audit.PresentedReport{}, fmt.Errorf("standard audit report is invalid")
	}
	return audit.PresentReport(&cloned, audit.ProfileStandard, s.deps.Audit.SyncInterval, s.deps.Audit.StandardInterval, s.auditNow()), nil
}

func (s *Server) auditNow() time.Time {
	if s.deps.Audit.Now != nil {
		return s.deps.Audit.Now().UTC()
	}
	return time.Now().UTC()
}

func cloneAuditReport(report audit.Report) (audit.Report, error) {
	data, err := json.Marshal(report)
	if err != nil {
		return audit.Report{}, err
	}
	var clone audit.Report
	if err := json.Unmarshal(data, &clone); err != nil {
		return audit.Report{}, err
	}
	return clone, nil
}

func validateAuditPresentation(presented audit.PresentedReport, profile audit.Profile) error {
	if presented.Report != nil {
		if presented.Report.Profile != profile || !presented.Report.Scope.WholeSystem || presented.Report.Scope.Filtered {
			return fmt.Errorf("audit report does not match requested whole-system profile")
		}
		if err := audit.ValidateReport(*presented.Report); err != nil {
			return err
		}
	} else if profile != audit.ProfileStandard {
		return fmt.Errorf("fast audit report is missing")
	}
	data, err := json.Marshal(presented)
	if err != nil {
		return err
	}
	var value interface{}
	if err := json.Unmarshal(data, &value); err != nil {
		return err
	}
	return validateJSONSchemaValue(auditOutputSchema(), value)
}
