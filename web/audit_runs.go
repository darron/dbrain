package web

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"math"
	"sort"
	"sync"
	"time"

	"github.com/darron/dbrain/internal/audit"
)

const (
	auditRunRetention        = 24 * time.Hour
	auditRunCleanupInterval  = time.Hour
	auditRunTerminalLimit    = 100
	auditStandardMinInterval = time.Minute
)

type AuditReportReader interface {
	Latest(audit.Profile) (*audit.Report, error)
	History(audit.Profile, int) ([]audit.Report, error)
}

type AuditReportWriter interface {
	Save(audit.Report) error
}

type AuditRunFunc func(context.Context, audit.Profile) (audit.Report, error)

type AuditRunCoordinatorOptions struct {
	Runner           AuditRunFunc
	Reports          AuditReportWriter
	SyncInterval     time.Duration
	StandardInterval time.Duration
	Now              func() time.Time
	CleanupInterval  time.Duration
}

type auditRunRecord struct {
	AuditID    string
	Profile    audit.Profile
	State      AuditRunState
	StartedAt  time.Time
	TerminalAt time.Time
	ErrorCode  string
	Report     *audit.Report
}

type AuditRunStartKind int

const (
	AuditRunStarted AuditRunStartKind = iota
	AuditRunDeduplicated
	AuditRunConflict
	AuditRunRateLimited
	AuditRunUnavailable
)

type AuditRunStartResult struct {
	Kind              AuditRunStartKind
	Status            AuditRunStatusResponse
	ActiveAuditID     string
	ActiveProfile     audit.Profile
	RetryAfterSeconds int64
}

type AuditRunCoordinator struct {
	mu               sync.Mutex
	ctx              context.Context
	runner           AuditRunFunc
	reports          AuditReportWriter
	syncInterval     time.Duration
	standardInterval time.Duration
	now              func() time.Time
	cleanupInterval  time.Duration
	records          map[string]*auditRunRecord
	active           *auditRunRecord
	lastStandardAt   time.Time
	cleanupDone      chan struct{}
}

func NewAuditRunCoordinator(ctx context.Context, opts AuditRunCoordinatorOptions) *AuditRunCoordinator {
	if ctx == nil {
		ctx = context.Background()
	}
	now := opts.Now
	if now == nil {
		now = func() time.Time { return time.Now().UTC() }
	}
	cleanupInterval := opts.CleanupInterval
	if cleanupInterval <= 0 {
		cleanupInterval = auditRunCleanupInterval
	}
	c := &AuditRunCoordinator{
		ctx: ctx, runner: opts.Runner, reports: opts.Reports,
		syncInterval: opts.SyncInterval, standardInterval: opts.StandardInterval,
		now: now, cleanupInterval: cleanupInterval, records: make(map[string]*auditRunRecord),
		cleanupDone: make(chan struct{}),
	}
	if ctx.Done() == nil {
		close(c.cleanupDone)
	} else {
		go c.cleanupLoop()
	}
	return c
}

func (c *AuditRunCoordinator) Start(profile audit.Profile) AuditRunStartResult {
	if c == nil || c.runner == nil || c.reports == nil || (profile != audit.ProfileFast && profile != audit.ProfileStandard) {
		return AuditRunStartResult{Kind: AuditRunUnavailable}
	}
	if c.ctx.Err() != nil {
		return AuditRunStartResult{Kind: AuditRunUnavailable}
	}
	now := c.now().UTC()
	c.mu.Lock()
	if c.ctx.Err() != nil {
		c.mu.Unlock()
		return AuditRunStartResult{Kind: AuditRunUnavailable}
	}
	c.cleanupLocked(now)
	if c.active != nil {
		active := c.active
		if active.Profile == profile {
			status := c.statusLocked(active, now)
			c.mu.Unlock()
			return AuditRunStartResult{Kind: AuditRunDeduplicated, Status: status}
		}
		result := AuditRunStartResult{Kind: AuditRunConflict, ActiveAuditID: active.AuditID, ActiveProfile: active.Profile}
		c.mu.Unlock()
		return result
	}
	if profile == audit.ProfileStandard && !c.lastStandardAt.IsZero() {
		remaining := auditStandardMinInterval - now.Sub(c.lastStandardAt)
		if remaining > 0 {
			c.mu.Unlock()
			return AuditRunStartResult{Kind: AuditRunRateLimited, RetryAfterSeconds: int64(math.Ceil(remaining.Seconds()))}
		}
	}
	id, err := newAuditRunID()
	if err != nil {
		c.mu.Unlock()
		return AuditRunStartResult{Kind: AuditRunUnavailable}
	}
	record := &auditRunRecord{AuditID: id, Profile: profile, State: AuditRunRunning, StartedAt: now}
	c.records[id] = record
	c.active = record
	if profile == audit.ProfileStandard {
		c.lastStandardAt = now
	}
	status := c.statusLocked(record, now)
	c.mu.Unlock()
	go c.execute(record)
	return AuditRunStartResult{Kind: AuditRunStarted, Status: status}
}

func (c *AuditRunCoordinator) execute(record *auditRunRecord) {
	report, err := c.runner(c.ctx, record.Profile)
	if c.ctx.Err() != nil {
		c.finishFailed(record, AuditRunErrorFailed)
		return
	}
	if err != nil || audit.ValidateReport(report) != nil || report.Profile != record.Profile || !report.Scope.WholeSystem || report.Scope.Filtered {
		c.finishFailed(record, AuditRunErrorFailed)
		return
	}
	if err := c.reports.Save(report); err != nil {
		c.finishFailed(record, AuditRunErrorPersist)
		return
	}
	stored, err := cloneWebAuditReport(report)
	if err != nil {
		c.finishFailed(record, AuditRunErrorFailed)
		return
	}
	now := c.now().UTC()
	c.mu.Lock()
	if current := c.records[record.AuditID]; current == record && record.State == AuditRunRunning {
		record.Report = &stored
		record.State = AuditRunCompleted
		record.TerminalAt = now
		if c.active == record {
			c.active = nil
		}
		c.cleanupLocked(now)
	}
	c.mu.Unlock()
}

func (c *AuditRunCoordinator) finishFailed(record *auditRunRecord, code string) {
	now := c.now().UTC()
	c.mu.Lock()
	if current := c.records[record.AuditID]; current == record && record.State == AuditRunRunning {
		record.State = AuditRunFailed
		record.ErrorCode = code
		record.TerminalAt = now
		if c.active == record {
			c.active = nil
		}
		c.cleanupLocked(now)
	}
	c.mu.Unlock()
}

func (c *AuditRunCoordinator) Status(id string) (AuditRunStatusResponse, bool) {
	if c == nil {
		return AuditRunStatusResponse{}, false
	}
	now := c.now().UTC()
	c.mu.Lock()
	c.cleanupLocked(now)
	record, ok := c.records[id]
	if !ok {
		c.mu.Unlock()
		return AuditRunStatusResponse{}, false
	}
	status := c.statusLocked(record, now)
	c.mu.Unlock()
	return status, true
}

func (c *AuditRunCoordinator) statusLocked(record *auditRunRecord, now time.Time) AuditRunStatusResponse {
	out := AuditRunStatusResponse{
		AuditID: record.AuditID, Profile: record.Profile, State: record.State,
		StatusPath: "/api/audit/runs/" + record.AuditID, StartedAt: record.StartedAt,
		ErrorCode: record.ErrorCode,
	}
	if !record.TerminalAt.IsZero() {
		out.CompletedAt = record.TerminalAt
	}
	if record.State == AuditRunCompleted && record.Report != nil {
		report, err := cloneWebAuditReport(*record.Report)
		if err != nil {
			return AuditRunStatusResponse{
				AuditID: record.AuditID, Profile: record.Profile, State: AuditRunFailed,
				StatusPath: "/api/audit/runs/" + record.AuditID, StartedAt: record.StartedAt,
				CompletedAt: record.TerminalAt, ErrorCode: AuditRunErrorFailed,
			}
		}
		presented := audit.PresentReport(&report, record.Profile, c.syncInterval, c.standardInterval, now)
		out.Report = presented.Report
		freshness := presented.Freshness
		out.Freshness = &freshness
	}
	return out
}

func cloneWebAuditReport(report audit.Report) (audit.Report, error) {
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

func (c *AuditRunCoordinator) cleanupLocked(now time.Time) {
	terminal := make([]*auditRunRecord, 0, len(c.records))
	for id, record := range c.records {
		if record.State == AuditRunRunning {
			continue
		}
		if !record.TerminalAt.IsZero() && now.Sub(record.TerminalAt) >= auditRunRetention {
			delete(c.records, id)
			continue
		}
		terminal = append(terminal, record)
	}
	if len(terminal) <= auditRunTerminalLimit {
		return
	}
	sort.Slice(terminal, func(i, j int) bool {
		if terminal[i].TerminalAt.Equal(terminal[j].TerminalAt) {
			return terminal[i].AuditID < terminal[j].AuditID
		}
		return terminal[i].TerminalAt.Before(terminal[j].TerminalAt)
	})
	for _, record := range terminal[:len(terminal)-auditRunTerminalLimit] {
		delete(c.records, record.AuditID)
	}
}

func (c *AuditRunCoordinator) cleanupLoop() {
	defer close(c.cleanupDone)
	ticker := time.NewTicker(c.cleanupInterval)
	defer ticker.Stop()
	for {
		select {
		case <-c.ctx.Done():
			return
		case <-ticker.C:
			now := c.now().UTC()
			c.mu.Lock()
			c.cleanupLocked(now)
			c.mu.Unlock()
		}
	}
}

func newAuditRunID() (string, error) {
	var raw [16]byte
	if _, err := rand.Read(raw[:]); err != nil {
		return "", err
	}
	return "run_" + hex.EncodeToString(raw[:]), nil
}
