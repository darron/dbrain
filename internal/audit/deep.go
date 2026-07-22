package audit

import (
	"context"
	"errors"
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/darron/dbrain/internal/vaultfs"
)

const (
	DefaultDeepMaxArchiveBytes  int64 = 20 << 30
	DefaultDeepMaxDatabaseBytes int64 = 100 << 30
	DefaultDeepMaxTempBytes     int64 = 120 << 30
	DeepMaxObjects                    = 1_000_000
	DeepMaxPages                      = 10_000
	DeepMaxConcurrency                = 8
)

var (
	ErrDeepProfileRequired   = errors.New("deep audit entry point requires deep profile")
	ErrDeepBudget            = errors.New("deep audit budget exhausted")
	ErrDeepInterrupted       = errors.New("deep audit interrupted")
	ErrDeepCandidateInvalid  = errors.New("deep archive candidate invalid")
	ErrDeepListingIncomplete = errors.New("deep inventory listing incomplete")
)

type DeepLimits struct {
	MaxArchiveBytes  int64
	MaxDatabaseBytes int64
	MaxTempBytes     int64
	MaxObjects       int
	MaxPages         int
	MaxConcurrency   int
	RequestTimeout   time.Duration
	ReadIdleTimeout  time.Duration
	RunTimeout       time.Duration
}

func DefaultDeepLimits() DeepLimits {
	return DeepLimits{
		MaxArchiveBytes: DefaultDeepMaxArchiveBytes, MaxDatabaseBytes: DefaultDeepMaxDatabaseBytes,
		MaxTempBytes: DefaultDeepMaxTempBytes, MaxObjects: DeepMaxObjects, MaxPages: DeepMaxPages,
		MaxConcurrency: DeepMaxConcurrency, RequestTimeout: 30 * time.Second,
		ReadIdleTimeout: 60 * time.Second, RunTimeout: 2 * time.Hour,
	}
}

func normalizeDeepLimits(value DeepLimits) (DeepLimits, error) {
	defaults := DefaultDeepLimits()
	if value.MaxArchiveBytes == 0 {
		value.MaxArchiveBytes = defaults.MaxArchiveBytes
	}
	if value.MaxDatabaseBytes == 0 {
		value.MaxDatabaseBytes = defaults.MaxDatabaseBytes
	}
	if value.MaxTempBytes == 0 {
		value.MaxTempBytes = defaults.MaxTempBytes
	}
	if value.MaxObjects == 0 || value.MaxObjects > DeepMaxObjects {
		value.MaxObjects = DeepMaxObjects
	}
	if value.MaxPages == 0 || value.MaxPages > DeepMaxPages {
		value.MaxPages = DeepMaxPages
	}
	if value.MaxConcurrency == 0 || value.MaxConcurrency > DeepMaxConcurrency {
		value.MaxConcurrency = DeepMaxConcurrency
	}
	if value.RequestTimeout == 0 || value.RequestTimeout > 30*time.Second {
		value.RequestTimeout = 30 * time.Second
	}
	if value.ReadIdleTimeout == 0 || value.ReadIdleTimeout > 60*time.Second {
		value.ReadIdleTimeout = 60 * time.Second
	}
	if value.RunTimeout == 0 || value.RunTimeout > 2*time.Hour {
		value.RunTimeout = 2 * time.Hour
	}
	if value.MaxArchiveBytes <= 0 || value.MaxDatabaseBytes <= 0 || value.MaxTempBytes <= 0 || value.MaxObjects <= 0 || value.MaxPages <= 0 || value.MaxConcurrency <= 0 || value.RequestTimeout <= 0 || value.ReadIdleTimeout <= 0 || value.RunTimeout <= 0 {
		return DeepLimits{}, fmt.Errorf("deep audit limits must be positive")
	}
	return value, nil
}

type DeepArchiveReader interface {
	Open(context.Context, string) (io.ReadCloser, error)
}

type DeepArchiveResult struct {
	CompressedBytes          int64
	DecompressedBytes        int64
	QuickCheck               string
	QuickCheckObserved       bool
	ForeignKeyViolationCount int
	ForeignKeysObserved      bool
	SchemaCompatibility      string
	SchemaObserved           bool
	MigrationCompatibility   string
	MigrationObserved        bool
}

type DeepArchiveVerifier interface {
	Verify(context.Context, io.ReadCloser, *vaultfs.PrivateTemp, DeepLimits) (DeepArchiveResult, error)
}

type MediaInventoryObject struct {
	Key       string
	SizeBytes int64
}

type MediaInventoryPage struct {
	Objects   []MediaInventoryObject
	NextToken string
	Complete  bool
}

type DeepMediaInventory interface {
	ListPage(context.Context, string, int) (MediaInventoryPage, error)
}

type DeepDependencies struct {
	Archives             DeepArchiveReader
	VerifyArchive        DeepArchiveVerifier
	Media                DeepMediaInventory
	MediaErrorCode       ErrorCode
	NewTemp              func() (*vaultfs.PrivateTemp, error)
	CleanupTemp          func(*vaultfs.PrivateTemp) error
	RecordCleanupFailure func(string)
	FreeSpace            func(*vaultfs.PrivateTemp) (uint64, error)
	Limits               DeepLimits
	Upstream             UpstreamInventories
}

type deepMediaResult struct {
	population, checked             int
	recentPopulation, recentChecked int
	olderPopulation, olderChecked   int
	missing, mismatch, invalid      int
	remoteOnly, objects, pages      int
	complete                        bool
}

func RunDeep(ctx context.Context, req Request, deps Dependencies, deep DeepDependencies) (Report, error) {
	if req.Profile != ProfileDeep {
		return Report{}, ErrDeepProfileRequired
	}
	limits, err := normalizeDeepLimits(deep.Limits)
	if err != nil {
		return Report{}, err
	}
	deep.Limits = limits
	if err := deep.Upstream.validate(); err != nil {
		return Report{}, err
	}
	bounded, cancel := context.WithTimeout(ctx, limits.RunTimeout)
	defer cancel()
	return runAudit(bounded, req, deps, &deep)
}

func (s *runState) loadDeep(ctx context.Context) {
	if s.deep == nil {
		return
	}
	if deepSelected(s.req, CheckDurabilityMediaRemote) || deepSelected(s.req, CheckDurabilityMediaRemoteOnly) {
		s.loadDeepMedia(ctx)
	}
	if deepSelected(s.req, CheckDurabilitySQLiteRestore) && featureEnabled(mustLookup(CheckDurabilitySQLiteRestore), s.deps.Features, s.req) {
		s.loadDeepArchive(ctx)
	}
	s.loadDeepUpstream(ctx)
}

func (s *runState) loadDeepUpstream(ctx context.Context) {
	if s.deep == nil {
		return
	}
	budget := DefaultInventoryBudget()
	for _, entry := range Registry() {
		if ctx.Err() != nil {
			break
		}
		source, parity := upstreamCheckSources[entry.ID]
		if !parity || !deepSelected(s.req, entry.ID) || !featureEnabled(entry, s.deps.Features, s.req) {
			continue
		}
		inventory := s.deep.Upstream[source]
		if inventory == nil {
			s.upstream[source] = upstreamObservation{err: errCapabilityUnavailable, errorCode: ErrorUnavailable}
			continue
		}

		timeout := timeoutFor(ProfileDeep, TimeoutUpstreamInventory, s.deps.Features.Timeouts)
		sourceCtx, cancel := context.WithTimeout(ctx, timeout)
		value, inventoryErr := inventory.Inventory(sourceCtx, budget)
		if sourceCtx.Err() != nil {
			observation := upstreamObservation{
				result: boundedInventoryEvidence(value, budget), err: sourceCtx.Err(), errorCode: upstreamErrorCode(sourceCtx.Err()),
			}
			cancel()
			s.upstream[source] = observation
			continue
		}
		normalized, normalizeErr := normalizeInventoryResult(value, budget)
		if sourceCtx.Err() != nil {
			observation := upstreamObservation{
				result: boundedInventoryEvidence(value, budget), err: sourceCtx.Err(), errorCode: upstreamErrorCode(sourceCtx.Err()),
			}
			cancel()
			s.upstream[source] = observation
			continue
		}
		observation := upstreamObservation{result: normalized}
		switch {
		case normalizeErr != nil:
			observation.err = normalizeErr
			observation.errorCode = upstreamErrorCode(normalizeErr)
		case inventoryErr != nil:
			observation.result.Complete = false
			observation.err = inventoryErr
			observation.errorCode = upstreamErrorCode(inventoryErr)
		case !normalized.Complete:
			observation.err = ErrInventoryIncomplete
			observation.errorCode = ErrorListingIncomplete
		case s.deps.Store == nil:
			observation.result.Complete = false
			observation.err = errCapabilityUnavailable
			observation.errorCode = ErrorUnavailable
		default:
			matched, matchErr := s.deps.Store.CountLocalIdentityMatches(sourceCtx, source, normalized.IdentityHashes)
			if sourceCtx.Err() != nil {
				observation.result.Complete = false
				observation.err = sourceCtx.Err()
				observation.errorCode = upstreamErrorCode(sourceCtx.Err())
			} else if matchErr != nil {
				observation.result.Complete = false
				observation.err = matchErr
				observation.errorCode = ErrorDatabase
			} else if matched < 0 || matched > len(normalized.IdentityHashes) {
				observation.result.Complete = false
				observation.err = ErrInventoryInvalid
				observation.errorCode = ErrorDatabase
			} else {
				observation.matched = matched
			}
		}
		cancel()
		s.upstream[source] = observation
	}
}

func deepSelected(req Request, id CheckID) bool {
	entry, ok := Lookup(id)
	return ok && scopeIncludes(req, entry) && entry.InProfile(req.Profile)
}

func mustLookup(id CheckID) RegistryEntry {
	entry, _ := Lookup(id)
	return entry
}

func (s *runState) loadDeepMedia(ctx context.Context) {
	if !s.deps.Features.MediaRemoteEnabled {
		return
	}
	if s.mediaErr != nil {
		s.setDeepMediaError(deepMediaResult{}, errCapabilityUnavailable)
		return
	}
	if s.deep.Media == nil {
		code := s.deep.MediaErrorCode
		if !code.Valid() {
			code = s.deps.MediaErrorCode
		}
		if !code.Valid() {
			code = ErrorUnavailable
		}
		s.deepMediaErr = errCapabilityUnavailable
		s.deepMediaErrorCode = code
		return
	}
	result := deepMediaResult{population: len(s.media)}
	localRecords := make([]ArchivedMediaRecord, 0, len(s.media))
	localKeys := make(map[string]struct{}, len(s.media))
	cutoff := s.now.Add(-s.req.Since)
	for _, record := range s.media {
		key := strings.TrimSpace(record.Key)
		if key == "" {
			result.invalid++
			continue
		}
		record.Key = key
		localRecords = append(localRecords, record)
		localKeys[key] = struct{}{}
		if !record.ArchivedAtValid {
			result.invalid++
		} else if record.ArchivedAt.Before(cutoff) {
			result.olderPopulation++
		} else {
			result.recentPopulation++
		}
	}
	remote := make(map[string]int64)
	seenTokens := map[string]struct{}{}
	token := ""
	for {
		if result.pages >= s.deep.Limits.MaxPages || result.objects >= s.deep.Limits.MaxObjects {
			s.setDeepMediaError(result, ErrDeepBudget)
			return
		}
		remaining := s.deep.Limits.MaxObjects - result.objects
		if remaining > 1000 {
			remaining = 1000
		}
		requestCtx, cancel := context.WithTimeout(ctx, s.deep.Limits.RequestTimeout)
		page, err := s.deep.Media.ListPage(requestCtx, token, remaining)
		requestErr := requestCtx.Err()
		cancel()
		result.pages++
		if requestErr != nil {
			s.setDeepMediaError(result, requestErr)
			return
		}
		if err != nil {
			s.setDeepMediaError(result, err)
			return
		}
		if len(page.Objects) > remaining {
			s.setDeepMediaError(result, ErrDeepBudget)
			return
		}
		for _, object := range page.Objects {
			key := strings.TrimSpace(object.Key)
			if key == "" || object.SizeBytes < 0 {
				s.setDeepMediaError(result, ErrDeepListingIncomplete)
				return
			}
			if previous, exists := remote[key]; exists {
				if previous != object.SizeBytes {
					s.setDeepMediaError(result, ErrDeepListingIncomplete)
					return
				}
				s.setDeepMediaError(result, ErrDeepListingIncomplete)
				return
			}
			remote[key] = object.SizeBytes
			result.objects++
		}
		if page.Complete {
			result.complete = true
			break
		}
		if len(page.Objects) == 0 || strings.TrimSpace(page.NextToken) == "" {
			s.setDeepMediaError(result, ErrDeepListingIncomplete)
			return
		}
		next := strings.TrimSpace(page.NextToken)
		if _, exists := seenTokens[next]; exists {
			s.setDeepMediaError(result, ErrDeepListingIncomplete)
			return
		}
		seenTokens[next] = struct{}{}
		token = next
	}
	for _, record := range localRecords {
		size, exists := remote[record.Key]
		result.checked++
		if !record.ArchivedAtValid {
			// Timestamp validity is reported separately from key/size
			// reconciliation. It must not suppress durability coverage.
		} else if record.ArchivedAt.Before(cutoff) {
			result.olderChecked++
		} else {
			result.recentChecked++
		}
		if !exists {
			result.missing++
		} else if size != record.SizeBytes {
			result.mismatch++
		}
	}
	for key := range remote {
		if _, exists := localKeys[key]; !exists {
			result.remoteOnly++
		}
	}
	s.deepMedia = result
}

func (s *runState) setDeepMediaError(result deepMediaResult, err error) {
	s.deepMedia = result
	s.deepMediaErr = err
	s.deepMediaErrorCode = deepMediaFailureCode(err)
}

func deepMediaFailureCode(err error) ErrorCode {
	switch {
	case errors.Is(err, context.DeadlineExceeded):
		return ErrorTimeout
	case errors.Is(err, context.Canceled):
		return ErrorCanceled
	case errors.Is(err, ErrDeepBudget):
		return ErrorBudgetExhausted
	case errors.Is(err, ErrDeepListingIncomplete):
		return ErrorListingIncomplete
	case errors.Is(err, errCapabilityUnavailable):
		return ErrorUnavailable
	default:
		return ErrorRead
	}
}

func (s *runState) deepMediaAuditErrorCode() ErrorCode {
	if s.deepMediaErrorCode.Valid() {
		return s.deepMediaErrorCode
	}
	if !s.deepMedia.complete {
		return ErrorListingIncomplete
	}
	return ErrorUnavailable
}

func (s *runState) loadDeepArchive(ctx context.Context) {
	if s.archivesErr != nil || !s.archives.Complete || s.deep.Archives == nil || s.deep.VerifyArchive == nil || s.deep.NewTemp == nil {
		s.deepArchiveErr = errCapabilityUnavailable
		return
	}
	var newest ArchiveObject
	for _, object := range s.archives.Objects {
		if !object.ValidKey || object.SizeBytes <= 0 || object.LastModified.IsZero() || object.LastModified.After(s.now) {
			continue
		}
		if newest.Key == "" || object.LastModified.After(newest.LastModified) {
			newest = object
		}
	}
	if newest.Key == "" {
		s.deepArchiveErr = ErrDeepCandidateInvalid
		return
	}
	temp, err := s.deep.NewTemp()
	if err != nil {
		s.deepArchiveErr = err
		return
	}
	s.deepCleanupComplete = false
	s.deepCleanupAttempted = false
	freeSpace := s.deep.FreeSpace
	if freeSpace == nil {
		freeSpace = func(value *vaultfs.PrivateTemp) (uint64, error) { return value.AvailableBytes() }
	}
	available, err := freeSpace(temp)
	if err != nil || available < uint64(s.deep.Limits.MaxTempBytes) {
		if err == nil {
			err = ErrDeepBudget
		}
		s.deepArchiveErr = err
		s.cleanupDeepTemp(temp)
		return
	}
	body, err := s.deep.Archives.Open(ctx, newest.Key)
	if err != nil {
		s.deepArchiveErr = err
		s.cleanupDeepTemp(temp)
		return
	}
	s.deepArchive, s.deepArchiveErr = s.deep.VerifyArchive.Verify(ctx, body, temp, s.deep.Limits)
	_ = body.Close()
	s.cleanupDeepTemp(temp)
}

func (s *runState) cleanupDeepTemp(temp *vaultfs.PrivateTemp) {
	s.deepCleanupAttempted = true
	cleanup := s.deep.CleanupTemp
	if cleanup == nil {
		cleanup = func(value *vaultfs.PrivateTemp) error { return value.Cleanup() }
	}
	err := cleanup(temp)
	s.deepCleanupComplete = err == nil
	if err != nil && s.deep.RecordCleanupFailure != nil {
		s.deep.RecordCleanupFailure(temp.Dir())
	}
}

func executeDeep(_ context.Context, s *runState, entry RegistryEntry) Check {
	switch entry.ID {
	case CheckDurabilityMediaRemoteOnly:
		if s.deepMediaErr != nil || !s.deepMedia.complete {
			check := baseCheck(entry, s.now, StatusUnknown, ConfidenceUnknown, Evidence{"remote_only_count": s.deepMedia.remoteOnly, "inventory_complete": false})
			check.ErrorCode = s.deepMediaAuditErrorCode()
			return check
		}
		status := StatusPass
		if s.deepMedia.remoteOnly > 0 {
			status = StatusWarn
		}
		return baseCheck(entry, s.now, status, ConfidenceHigh, Evidence{"remote_only_count": s.deepMedia.remoteOnly, "inventory_complete": true})
	case CheckDurabilitySQLiteRestore:
		result := s.deepArchive
		evidence := Evidence{"archive_authenticity": "unverified"}
		if result.CompressedBytes > 0 {
			evidence["compressed_bytes"] = result.CompressedBytes
		}
		if result.DecompressedBytes > 0 {
			evidence["decompressed_bytes"] = result.DecompressedBytes
		}
		if result.QuickCheckObserved && (result.QuickCheck == "ok" || result.QuickCheck == "violation") {
			evidence["quick_check"] = result.QuickCheck
		}
		if result.ForeignKeysObserved {
			evidence["foreign_key_violation_count"] = result.ForeignKeyViolationCount
		}
		if result.SchemaObserved {
			if compatibility := normalizeCompatibility(result.SchemaCompatibility); compatibility != "" {
				evidence["schema_compatibility"] = compatibility
			}
		}
		if result.MigrationObserved {
			if compatibility := normalizeCompatibility(result.MigrationCompatibility); compatibility != "" {
				evidence["migration_compatibility"] = compatibility
			}
		}
		if s.deepCleanupAttempted {
			evidence["cleanup_complete"] = s.deepCleanupComplete
		}
		if s.deepArchiveErr != nil {
			if errors.Is(s.deepArchiveErr, ErrDeepCandidateInvalid) {
				return baseCheck(entry, s.now, StatusFail, ConfidenceHigh, evidence)
			}
			return baseCheck(entry, s.now, StatusUnknown, ConfidenceUnknown, evidence)
		}
		if s.deepCleanupAttempted && !s.deepCleanupComplete {
			return baseCheck(entry, s.now, StatusUnknown, ConfidenceUnknown, evidence)
		}
		return baseCheck(entry, s.now, StatusPass, ConfidenceHigh, evidence)
	}
	return unknownCheck(entry, ErrorUnavailable, s.now)
}

func normalizeCompatibility(value string) string {
	if value == "current_compatible" || value == "legacy_compatible" || value == "incompatible" {
		return value
	}
	return ""
}
