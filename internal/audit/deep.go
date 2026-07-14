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
	ForeignKeyViolationCount int
	SchemaCompatibility      string
	MigrationCompatibility   string
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
	Archives      DeepArchiveReader
	VerifyArchive DeepArchiveVerifier
	Media         DeepMediaInventory
	NewTemp       func() (*vaultfs.PrivateTemp, error)
	FreeSpace     func(*vaultfs.PrivateTemp) (uint64, error)
	Limits        DeepLimits
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
	if deepSelected(s.req, CheckDurabilitySQLiteRestore) && featureEnabled(mustLookup(CheckDurabilitySQLiteRestore), s.deps.Features) {
		s.loadDeepArchive(ctx)
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
	if s.deep.Media == nil || s.mediaErr != nil {
		s.deepMediaErr = errCapabilityUnavailable
		return
	}
	result := deepMediaResult{population: len(s.media)}
	localRecords := make([]ArchivedMediaRecord, 0, len(s.media))
	localKeys := make(map[string]struct{}, len(s.media))
	cutoff := s.now.Add(-s.req.Since)
	for _, record := range s.media {
		if !record.ArchivedAtValid || strings.TrimSpace(record.Key) == "" {
			result.invalid++
			continue
		}
		localRecords = append(localRecords, record)
		localKeys[record.Key] = struct{}{}
		if record.ArchivedAt.Before(cutoff) {
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
			s.deepMedia = result
			s.deepMediaErr = ErrDeepBudget
			return
		}
		remaining := s.deep.Limits.MaxObjects - result.objects
		if remaining > 1000 {
			remaining = 1000
		}
		requestCtx, cancel := context.WithTimeout(ctx, s.deep.Limits.RequestTimeout)
		page, err := s.deep.Media.ListPage(requestCtx, token, remaining)
		cancel()
		result.pages++
		if err != nil {
			s.deepMedia, s.deepMediaErr = result, err
			return
		}
		if len(page.Objects) > remaining {
			s.deepMedia, s.deepMediaErr = result, ErrDeepBudget
			return
		}
		for _, object := range page.Objects {
			key := strings.TrimSpace(object.Key)
			if key == "" || object.SizeBytes < 0 {
				s.deepMedia, s.deepMediaErr = result, ErrDeepListingIncomplete
				return
			}
			if previous, exists := remote[key]; exists {
				if previous != object.SizeBytes {
					s.deepMedia, s.deepMediaErr = result, ErrDeepListingIncomplete
					return
				}
				s.deepMedia, s.deepMediaErr = result, ErrDeepListingIncomplete
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
			s.deepMedia, s.deepMediaErr = result, ErrDeepListingIncomplete
			return
		}
		next := strings.TrimSpace(page.NextToken)
		if _, exists := seenTokens[next]; exists {
			s.deepMedia, s.deepMediaErr = result, ErrDeepListingIncomplete
			return
		}
		seenTokens[next] = struct{}{}
		token = next
	}
	for _, record := range localRecords {
		size, exists := remote[record.Key]
		result.checked++
		if record.ArchivedAt.Before(cutoff) {
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
		s.deepCleanupComplete = temp.Cleanup() == nil
		return
	}
	body, err := s.deep.Archives.Open(ctx, newest.Key)
	if err != nil {
		s.deepArchiveErr = err
		s.deepCleanupComplete = temp.Cleanup() == nil
		return
	}
	s.deepArchive, s.deepArchiveErr = s.deep.VerifyArchive.Verify(ctx, body, temp, s.deep.Limits)
	_ = body.Close()
	s.deepCleanupComplete = temp.Cleanup() == nil
}

func executeDeep(_ context.Context, s *runState, entry RegistryEntry) Check {
	switch entry.ID {
	case CheckDurabilityMediaRemoteOnly:
		if s.deepMediaErr != nil || !s.deepMedia.complete {
			return baseCheck(entry, s.now, StatusUnknown, ConfidenceUnknown, Evidence{"remote_only_count": s.deepMedia.remoteOnly, "inventory_complete": false})
		}
		status := StatusPass
		if s.deepMedia.remoteOnly > 0 {
			status = StatusWarn
		}
		return baseCheck(entry, s.now, status, ConfidenceHigh, Evidence{"remote_only_count": s.deepMedia.remoteOnly, "inventory_complete": true})
	case CheckDurabilitySQLiteRestore:
		result := s.deepArchive
		quick := result.QuickCheck
		if quick != "ok" && quick != "violation" {
			quick = "violation"
		}
		schema := normalizeCompatibility(result.SchemaCompatibility)
		migration := normalizeCompatibility(result.MigrationCompatibility)
		evidence := Evidence{
			"compressed_bytes": result.CompressedBytes, "decompressed_bytes": result.DecompressedBytes,
			"quick_check": quick, "foreign_key_violation_count": result.ForeignKeyViolationCount,
			"schema_compatibility": schema, "migration_compatibility": migration,
			"archive_authenticity": "unverified", "cleanup_complete": s.deepCleanupComplete,
		}
		if s.deepArchiveErr != nil {
			if errors.Is(s.deepArchiveErr, ErrDeepCandidateInvalid) {
				return baseCheck(entry, s.now, StatusFail, ConfidenceHigh, evidence)
			}
			return baseCheck(entry, s.now, StatusUnknown, ConfidenceUnknown, evidence)
		}
		if !s.deepCleanupComplete {
			return baseCheck(entry, s.now, StatusWarn, ConfidenceHigh, evidence)
		}
		return baseCheck(entry, s.now, StatusPass, ConfidenceHigh, evidence)
	}
	return unknownCheck(entry, ErrorUnavailable, s.now)
}

func normalizeCompatibility(value string) string {
	if value == "current_compatible" || value == "legacy_compatible" || value == "incompatible" {
		return value
	}
	return "incompatible"
}
