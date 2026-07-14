package audit

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/darron/dbrain/internal/vaultfs"
)

type fakeDeepArchiveReader struct {
	body  []byte
	err   error
	calls int
}

func (r *fakeDeepArchiveReader) Open(context.Context, string) (io.ReadCloser, error) {
	r.calls++
	if r.err != nil {
		return nil, r.err
	}
	return io.NopCloser(bytes.NewReader(r.body)), nil
}

type fakeDeepVerifier struct {
	result DeepArchiveResult
	err    error
	calls  int
}

func failingDeepCleanup(*vaultfs.PrivateTemp) error { return errors.New("injected cleanup failure") }

type cancelDeepVerifier struct{}

func (cancelDeepVerifier) Verify(ctx context.Context, _ io.ReadCloser, _ *vaultfs.PrivateTemp, _ DeepLimits) (DeepArchiveResult, error) {
	<-ctx.Done()
	return DeepArchiveResult{}, fmt.Errorf("%w: %v", ErrDeepInterrupted, ctx.Err())
}

func (v *fakeDeepVerifier) Verify(context.Context, io.ReadCloser, *vaultfs.PrivateTemp, DeepLimits) (DeepArchiveResult, error) {
	v.calls++
	return v.result, v.err
}

type fakeDeepInventory struct {
	pages     []MediaInventoryPage
	calls     int
	remaining []time.Duration
}

type remoteOnlyCountingStore struct {
	fakeStore
	archivedCalls int
}

func (s *remoteOnlyCountingStore) ArchivedMedia(context.Context) ([]ArchivedMediaRecord, error) {
	s.archivedCalls++
	return s.fakeStore.ArchivedMedia(context.Background())
}

func TestRunDeepRemoteOnlyLoadsArchivedMediaPopulation(t *testing.T) {
	now := time.Date(2026, 7, 14, 12, 0, 0, 0, time.UTC)
	deps := passingDependencies(now)
	store := &remoteOnlyCountingStore{fakeStore: deps.Store.(fakeStore)}
	deps.Store = store
	_, err := RunDeep(t.Context(), Request{Profile: ProfileDeep, CheckIDs: []CheckID{CheckDurabilityMediaRemoteOnly}}, deps, DeepDependencies{
		Media: &fakeDeepInventory{pages: []MediaInventoryPage{{Complete: true}}}, Limits: DefaultDeepLimits(),
	})
	if err != nil {
		t.Fatal(err)
	}
	if store.archivedCalls != 1 {
		t.Fatalf("remote-only archived media calls = %d, want 1", store.archivedCalls)
	}
}

type delayedDeepInventory struct {
	delay      time.Duration
	page       MediaInventoryPage
	calls      int
	contextErr error
}

func (f *delayedDeepInventory) ListPage(ctx context.Context, _ string, _ int) (MediaInventoryPage, error) {
	f.calls++
	timer := time.NewTimer(f.delay)
	defer timer.Stop()
	select {
	case <-timer.C:
		return f.page, nil
	case <-ctx.Done():
		f.contextErr = ctx.Err()
		return MediaInventoryPage{}, ctx.Err()
	}
}

type delayedDeepArchiveListing struct {
	delay      time.Duration
	value      SQLiteArchiveListing
	calls      int
	completed  bool
	contextErr error
}

func (f *delayedDeepArchiveListing) List(ctx context.Context) (SQLiteArchiveListing, error) {
	f.calls++
	timer := time.NewTimer(f.delay)
	defer timer.Stop()
	select {
	case <-timer.C:
		f.completed = true
		return f.value, nil
	case <-ctx.Done():
		f.contextErr = ctx.Err()
		return SQLiteArchiveListing{}, ctx.Err()
	}
}

func (f *fakeDeepInventory) ListPage(ctx context.Context, _ string, _ int) (MediaInventoryPage, error) {
	if deadline, ok := ctx.Deadline(); ok {
		f.remaining = append(f.remaining, time.Until(deadline))
	}
	if f.calls >= len(f.pages) {
		return MediaInventoryPage{}, errors.New("unexpected inventory page")
	}
	page := f.pages[f.calls]
	f.calls++
	return page, nil
}

func TestDeepLimitsClampNonRaiseableResourceCeilings(t *testing.T) {
	limits, err := normalizeDeepLimits(DeepLimits{
		MaxArchiveBytes: 1, MaxDatabaseBytes: 1, MaxTempBytes: 1,
		MaxObjects: DeepMaxObjects + 1, MaxPages: DeepMaxPages + 1, MaxConcurrency: DeepMaxConcurrency + 1,
		RequestTimeout: time.Minute, ReadIdleTimeout: 2 * time.Minute, RunTimeout: 3 * time.Hour,
	})
	if err != nil {
		t.Fatal(err)
	}
	if limits.MaxObjects != DeepMaxObjects || limits.MaxPages != DeepMaxPages || limits.MaxConcurrency != DeepMaxConcurrency || limits.RequestTimeout != 30*time.Second || limits.ReadIdleTimeout != 60*time.Second || limits.RunTimeout != 2*time.Hour {
		t.Fatalf("normalized limits = %#v", limits)
	}
}

func TestDeepAuthorityIsSeparateFromOrdinaryRun(t *testing.T) {
	if _, err := Run(t.Context(), Request{Profile: ProfileDeep}, Dependencies{}); !errors.Is(err, ErrDeepUnsupported) {
		t.Fatalf("ordinary deep error = %v", err)
	} else if !strings.Contains(err.Error(), "RunDeep") || !strings.Contains(err.Error(), "--profile deep") {
		t.Fatalf("ordinary deep error does not identify the explicit entry point: %q", err)
	}
	if _, err := RunDeep(t.Context(), Request{Profile: ProfileStandard}, Dependencies{}, DeepDependencies{}); !errors.Is(err, ErrDeepProfileRequired) {
		t.Fatalf("standard through deep entry point error = %v", err)
	}
	typeOfDependencies := reflect.TypeOf(Dependencies{})
	for index := 0; index < typeOfDependencies.NumField(); index++ {
		field := typeOfDependencies.Field(index)
		for _, forbidden := range []string{"archivereader", "writer", "restore", "getobject", "putobject", "deleteobject"} {
			if strings.Contains(strings.ToLower(field.Name), forbidden) || strings.Contains(strings.ToLower(field.Type.String()), forbidden) {
				t.Fatalf("ordinary dependency exposes deep/write authority: %s %s", field.Name, field.Type)
			}
		}
	}
}

func TestRunDeepMediaInventoryOutlivesStandardRemoteMetadataTimeout(t *testing.T) {
	now := time.Date(2026, 7, 14, 12, 0, 0, 0, time.UTC)
	deps := passingDependencies(now)
	deps.Features.MediaRemoteEnabled = true
	deps.Features.Timeouts = map[TimeoutClass]time.Duration{TimeoutRemoteMetadata: 2 * time.Millisecond}
	deps.Store = fakeStore{media: []ArchivedMediaRecord{{Key: "media/a", SizeBytes: 10, ArchivedAt: now.Add(-time.Hour), ArchivedAtValid: true}}}
	inventory := &delayedDeepInventory{
		delay: 20 * time.Millisecond,
		page:  MediaInventoryPage{Objects: []MediaInventoryObject{{Key: "media/a", SizeBytes: 10}}, Complete: true},
	}
	limits := DefaultDeepLimits()
	limits.RequestTimeout = 100 * time.Millisecond
	limits.RunTimeout = 500 * time.Millisecond
	report, err := RunDeep(t.Context(), Request{Profile: ProfileDeep, CheckIDs: []CheckID{CheckDurabilityMediaRemote}}, deps, DeepDependencies{Media: inventory, Limits: limits})
	if err != nil {
		t.Fatal(err)
	}
	if inventory.calls != 1 || inventory.contextErr != nil {
		t.Fatalf("inventory calls=%d context_err=%v", inventory.calls, inventory.contextErr)
	}
	if report.Checks[0].Status != StatusPass {
		t.Fatalf("deep media check = %#v", report.Checks[0])
	}
}

func TestRunDeepArchiveListingOutlivesStandardRemoteMetadataTimeout(t *testing.T) {
	now := time.Date(2026, 7, 14, 12, 0, 0, 0, time.UTC)
	deps := passingDependencies(now)
	deps.Features.Timeouts = map[TimeoutClass]time.Duration{TimeoutRemoteMetadata: 2 * time.Millisecond}
	listing := &delayedDeepArchiveListing{
		delay: 20 * time.Millisecond,
		value: SQLiteArchiveListing{Complete: true, Objects: []ArchiveObject{{
			Key: "archive/db/brain-20260714T110000Z.db.gz", ValidKey: true, SizeBytes: 1, LastModified: now.Add(-time.Hour),
		}}},
	}
	deps.Archives = listing
	limits := DefaultDeepLimits()
	limits.RunTimeout = 500 * time.Millisecond
	_, err := RunDeep(t.Context(), Request{Profile: ProfileDeep, CheckIDs: []CheckID{CheckDurabilitySQLiteRestore}}, deps, DeepDependencies{Limits: limits})
	if err != nil {
		t.Fatal(err)
	}
	if listing.calls != 1 || !listing.completed || listing.contextErr != nil {
		t.Fatalf("archive listing calls=%d completed=%v context_err=%v", listing.calls, listing.completed, listing.contextErr)
	}
}

func TestRunDeepMediaInventoryIsBoundedByRunTimeout(t *testing.T) {
	now := time.Date(2026, 7, 14, 12, 0, 0, 0, time.UTC)
	deps := passingDependencies(now)
	deps.Features.MediaRemoteEnabled = true
	deps.Store = fakeStore{media: []ArchivedMediaRecord{{Key: "media/a", SizeBytes: 10, ArchivedAt: now.Add(-time.Hour), ArchivedAtValid: true}}}
	inventory := &delayedDeepInventory{delay: time.Hour}
	limits := DefaultDeepLimits()
	limits.RequestTimeout = time.Second
	limits.RunTimeout = 20 * time.Millisecond
	started := time.Now()
	report, err := RunDeep(t.Context(), Request{Profile: ProfileDeep, CheckIDs: []CheckID{CheckDurabilityMediaRemote}}, deps, DeepDependencies{Media: inventory, Limits: limits})
	if err != nil {
		t.Fatal(err)
	}
	if elapsed := time.Since(started); elapsed > 500*time.Millisecond {
		t.Fatalf("RunDeep exceeded run timeout bound: %s", elapsed)
	}
	if !errors.Is(inventory.contextErr, context.DeadlineExceeded) {
		t.Fatalf("inventory context error = %v", inventory.contextErr)
	}
	if report.Checks[0].Status != StatusUnknown {
		t.Fatalf("timed-out deep media check = %#v", report.Checks[0])
	}
}

func TestRunDeepReconcilesCompleteMediaInventoryWithoutKeysInEvidence(t *testing.T) {
	now := time.Date(2026, 7, 14, 12, 0, 0, 0, time.UTC)
	deps := passingDependencies(now)
	deps.Features.MediaRemoteEnabled = true
	deps.Store = fakeStore{media: []ArchivedMediaRecord{
		{Key: "media/a", SizeBytes: 10, ArchivedAt: now.Add(-time.Hour), ArchivedAtValid: true},
		{Key: "media/b", SizeBytes: 20, ArchivedAt: now.Add(-time.Hour), ArchivedAtValid: true},
	}}
	inventory := &fakeDeepInventory{pages: []MediaInventoryPage{{
		Objects: []MediaInventoryObject{{Key: "media/a", SizeBytes: 10}, {Key: "media/b", SizeBytes: 21}, {Key: "media/remote-only", SizeBytes: 30}}, Complete: true,
	}}}
	report, err := RunDeep(t.Context(), Request{Profile: ProfileDeep, Since: 7 * 24 * time.Hour, CheckIDs: []CheckID{CheckDurabilityMediaRemote, CheckDurabilityMediaRemoteOnly}}, deps, DeepDependencies{
		Media: inventory, Limits: DefaultDeepLimits(),
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(inventory.remaining) != 1 || inventory.remaining[0] > 30*time.Second || inventory.remaining[0] < 29*time.Second {
		t.Fatalf("metadata request deadlines = %v", inventory.remaining)
	}
	remote := report.Checks[0]
	if remote.Status != StatusFail || remote.Evidence["sample_mode"] != "full_inventory" || remote.Evidence["size_mismatch_count"] != 1 || remote.Evidence["inventory_complete"] != true {
		t.Fatalf("remote check = %#v", remote)
	}
	remoteOnly := report.Checks[1]
	if remoteOnly.Status != StatusWarn || remoteOnly.Required || remoteOnly.Evidence["remote_only_count"] != 1 {
		t.Fatalf("remote-only check = %#v", remoteOnly)
	}
	data, err := json.Marshal(report)
	if err != nil {
		t.Fatal(err)
	}
	for _, secret := range []string{"media/a", "media/b", "remote-only"} {
		if strings.Contains(string(data), secret) {
			t.Fatalf("shared report leaked object key %q: %s", secret, data)
		}
	}
}

func TestRunDeepReconcilesKeysEvenWhenArchiveTimestampIsInvalid(t *testing.T) {
	now := time.Date(2026, 7, 14, 12, 0, 0, 0, time.UTC)
	deps := passingDependencies(now)
	deps.Features.MediaRemoteEnabled = true
	deps.Store = fakeStore{media: []ArchivedMediaRecord{
		{Key: "media/matching", SizeBytes: 10, ArchivedAtValid: false},
		{Key: "media/missing", SizeBytes: 20, ArchivedAtValid: false},
		{Key: "media/mismatch", SizeBytes: 30, ArchivedAtValid: false},
	}}
	inventory := &fakeDeepInventory{pages: []MediaInventoryPage{{Objects: []MediaInventoryObject{
		{Key: "media/matching", SizeBytes: 10},
		{Key: "media/mismatch", SizeBytes: 31},
	}, Complete: true}}}
	report, err := RunDeep(t.Context(), Request{Profile: ProfileDeep, Since: 7 * 24 * time.Hour, CheckIDs: []CheckID{CheckDurabilityMediaRemote, CheckDurabilityMediaRemoteOnly}}, deps, DeepDependencies{Media: inventory, Limits: DefaultDeepLimits()})
	if err != nil {
		t.Fatal(err)
	}
	remote := report.Checks[0]
	if remote.Status != StatusFail || remote.Evidence["invalid_timestamp_count"] != 3 || remote.Evidence["checked_count"] != 3 || remote.Evidence["missing_count"] != 1 || remote.Evidence["size_mismatch_count"] != 1 {
		t.Fatalf("remote reconciliation = %#v", remote)
	}
	remoteOnly := report.Checks[1]
	if remoteOnly.Evidence["remote_only_count"] != 0 {
		t.Fatalf("invalid-timestamp local keys leaked into remote-only: %#v", remoteOnly)
	}
}

func TestRunDeepReconcilesEveryDuplicateLocalMediaRecord(t *testing.T) {
	now := time.Date(2026, 7, 14, 12, 0, 0, 0, time.UTC)
	deps := passingDependencies(now)
	deps.Features.MediaRemoteEnabled = true
	deps.Store = fakeStore{media: []ArchivedMediaRecord{
		{Key: "media/duplicate", SizeBytes: 20, ArchivedAt: now.Add(-2 * time.Hour), ArchivedAtValid: true},
		{Key: "media/duplicate", SizeBytes: 10, ArchivedAt: now.Add(-time.Hour), ArchivedAtValid: true},
	}}
	inventory := &fakeDeepInventory{pages: []MediaInventoryPage{{
		Objects: []MediaInventoryObject{{Key: "media/duplicate", SizeBytes: 10}}, Complete: true,
	}}}
	report, err := RunDeep(t.Context(), Request{Profile: ProfileDeep, Since: 7 * 24 * time.Hour, CheckIDs: []CheckID{CheckDurabilityMediaRemote, CheckDurabilityMediaRemoteOnly}}, deps, DeepDependencies{
		Media: inventory, Limits: DefaultDeepLimits(),
	})
	if err != nil {
		t.Fatal(err)
	}
	remote := report.Checks[0]
	if remote.Status != StatusFail || remote.Evidence["checked_count"] != 2 || remote.Evidence["size_mismatch_count"] != 1 {
		t.Fatalf("duplicate local media reconciliation = %#v", remote)
	}
	if report.Checks[1].Evidence["remote_only_count"] != 0 {
		t.Fatalf("duplicate local key misclassified as remote-only: %#v", report.Checks[1])
	}
}

func TestRunDeepReturnsUnknownForIncompleteInventoryBudgets(t *testing.T) {
	now := time.Date(2026, 7, 14, 12, 0, 0, 0, time.UTC)
	deps := passingDependencies(now)
	deps.Features.MediaRemoteEnabled = true
	deps.Store = fakeStore{media: []ArchivedMediaRecord{{Key: "media/a", SizeBytes: 10, ArchivedAt: now, ArchivedAtValid: true}}}
	inventory := &fakeDeepInventory{pages: []MediaInventoryPage{{Objects: []MediaInventoryObject{{Key: "media/a", SizeBytes: 10}}, NextToken: "next", Complete: false}}}
	limits := DefaultDeepLimits()
	limits.MaxPages = 1
	report, err := RunDeep(t.Context(), Request{Profile: ProfileDeep, CheckIDs: []CheckID{CheckDurabilityMediaRemote, CheckDurabilityMediaRemoteOnly}}, deps, DeepDependencies{Media: inventory, Limits: limits})
	if err != nil {
		t.Fatal(err)
	}
	for _, check := range report.Checks {
		if check.Status != StatusUnknown || check.Evidence["inventory_complete"] != false {
			t.Fatalf("incomplete check = %#v", check)
		}
	}
}

func TestRunDeepFindsMissingMediaOutsideStandardSample(t *testing.T) {
	now := time.Date(2026, 7, 14, 12, 0, 0, 0, time.UTC)
	records := make([]ArchivedMediaRecord, 0, 501)
	for index := 0; index < 501; index++ {
		records = append(records, ArchivedMediaRecord{Key: fmt.Sprintf("media/%03d", index), SizeBytes: 10, ArchivedAt: now.Add(-time.Hour), ArchivedAtValid: true})
	}
	standard := SelectMediaSample(records, 7*24*time.Hour, now, "test")
	selected := make(map[string]bool, len(standard.Records))
	for _, record := range standard.Records {
		selected[record.Key] = true
	}
	missing := ""
	objects := make([]MediaInventoryObject, 0, 500)
	for _, record := range records {
		if !selected[record.Key] && missing == "" {
			missing = record.Key
			continue
		}
		objects = append(objects, MediaInventoryObject{Key: record.Key, SizeBytes: record.SizeBytes})
	}
	if missing == "" {
		t.Fatal("fixture did not produce a record outside the standard sample")
	}
	deps := passingDependencies(now)
	deps.Features.MediaRemoteEnabled = true
	deps.Store = fakeStore{media: records}
	report, err := RunDeep(t.Context(), Request{Profile: ProfileDeep, Since: 7 * 24 * time.Hour, CheckIDs: []CheckID{CheckDurabilityMediaRemote}}, deps, DeepDependencies{
		Media: &fakeDeepInventory{pages: []MediaInventoryPage{{Objects: objects, Complete: true}}}, Limits: DefaultDeepLimits(),
	})
	if err != nil {
		t.Fatal(err)
	}
	if report.Checks[0].Status != StatusFail || report.Checks[0].Evidence["missing_count"] != 1 || report.Checks[0].Evidence["checked_count"] != 501 {
		t.Fatalf("deep remote check = %#v", report.Checks[0])
	}
}

func TestRunDeepCleansGeneratedTempOnArchiveSuccessAndFailure(t *testing.T) {
	for _, verifierErr := range []error{nil, errors.New("invalid candidate")} {
		t.Run(map[bool]string{true: "failure", false: "success"}[verifierErr != nil], func(t *testing.T) {
			now := time.Date(2026, 7, 14, 12, 0, 0, 0, time.UTC)
			deps := passingDependencies(now)
			deps.Features.SQLiteBackupSchedulerEnabled = true
			deps.Features.SQLiteProviderConfigured = true
			deps.Features.SQLiteCredentialConfigured = true
			deps.Archives = fakeArchive{value: SQLiteArchiveListing{Complete: true, Objects: []ArchiveObject{{Key: "archive/db/brain-20260714T110000Z.db.gz", ValidKey: true, SizeBytes: 1, LastModified: now.Add(-time.Hour)}}}}
			base := t.TempDir()
			var generated string
			factory := func() (*vaultfs.PrivateTemp, error) {
				tmp, err := vaultfs.NewPrivateTemp(base)
				if tmp != nil {
					generated = tmp.Dir()
				}
				return tmp, err
			}
			verifier := &fakeDeepVerifier{result: DeepArchiveResult{CompressedBytes: 1, DecompressedBytes: 2, QuickCheck: "ok", QuickCheckObserved: true, ForeignKeysObserved: true, SchemaCompatibility: "current_compatible", SchemaObserved: true, MigrationCompatibility: "current_compatible", MigrationObserved: true}, err: verifierErr}
			report, err := RunDeep(t.Context(), Request{Profile: ProfileDeep, CheckIDs: []CheckID{CheckDurabilitySQLiteRestore}}, deps, DeepDependencies{
				Archives: &fakeDeepArchiveReader{body: []byte("x")}, VerifyArchive: verifier, NewTemp: factory,
				FreeSpace: func(*vaultfs.PrivateTemp) (uint64, error) { return uint64(DefaultDeepLimits().MaxTempBytes), nil }, Limits: DefaultDeepLimits(),
			})
			if err != nil {
				t.Fatal(err)
			}
			if generated == "" {
				t.Fatal("deep temp was not generated")
			}
			if _, statErr := os.Stat(generated); !errors.Is(statErr, os.ErrNotExist) {
				t.Fatalf("generated temp remains: %v", statErr)
			}
			if report.Checks[0].Evidence["cleanup_complete"] != true {
				t.Fatalf("restore evidence = %#v", report.Checks[0])
			}
		})
	}
}

func TestValidateReportRejectsPassingRestoreWithMissingPhaseEvidence(t *testing.T) {
	now := time.Date(2026, 7, 14, 12, 0, 0, 0, time.UTC)
	deps := passingDependencies(now)
	deps.Features.SQLiteBackupSchedulerEnabled = true
	deps.Features.SQLiteProviderConfigured = true
	deps.Features.SQLiteCredentialConfigured = true
	deps.Archives = fakeArchive{value: SQLiteArchiveListing{Complete: true, Objects: []ArchiveObject{{Key: "archive/db/brain-20260714T110000Z.db.gz", ValidKey: true, SizeBytes: 1, LastModified: now.Add(-time.Hour)}}}}
	report, err := RunDeep(t.Context(), Request{Profile: ProfileDeep, CheckIDs: []CheckID{CheckDurabilitySQLiteRestore}}, deps, DeepDependencies{
		Archives:      &fakeDeepArchiveReader{body: []byte("x")},
		VerifyArchive: &fakeDeepVerifier{result: DeepArchiveResult{CompressedBytes: 1, DecompressedBytes: 2, QuickCheck: "ok", QuickCheckObserved: true, ForeignKeysObserved: true, SchemaCompatibility: "current_compatible", SchemaObserved: true, MigrationCompatibility: "current_compatible", MigrationObserved: true}},
		NewTemp:       func() (*vaultfs.PrivateTemp, error) { return vaultfs.NewPrivateTemp(t.TempDir()) },
		FreeSpace:     func(*vaultfs.PrivateTemp) (uint64, error) { return uint64(DefaultDeepLimits().MaxTempBytes), nil }, Limits: DefaultDeepLimits(),
	})
	if err != nil {
		t.Fatal(err)
	}
	for _, key := range []string{"quick_check", "schema_compatibility", "migration_compatibility", "cleanup_complete"} {
		mutated := report
		mutated.Checks = append([]Check(nil), report.Checks...)
		mutated.Checks[0].Evidence = Evidence{}
		for evidenceKey, value := range report.Checks[0].Evidence {
			if evidenceKey != key {
				mutated.Checks[0].Evidence[evidenceKey] = value
			}
		}
		if err := ValidateReport(mutated); err == nil {
			t.Fatalf("passing restore accepted missing %s", key)
		}
	}
}

func TestRunDeepCleanupFailureMakesRestoreUnknownAndExposesOnlyLocalCleanupPath(t *testing.T) {
	now := time.Date(2026, 7, 14, 12, 0, 0, 0, time.UTC)
	deps := passingDependencies(now)
	deps.Features.SQLiteBackupSchedulerEnabled = true
	deps.Features.SQLiteProviderConfigured = true
	deps.Features.SQLiteCredentialConfigured = true
	deps.Archives = fakeArchive{value: SQLiteArchiveListing{Complete: true, Objects: []ArchiveObject{{Key: "archive/db/brain-20260714T110000Z.db.gz", ValidKey: true, SizeBytes: 1, LastModified: now.Add(-time.Hour)}}}}
	base := t.TempDir()
	var generated string
	var cleanupFailures []string
	report, err := RunDeep(t.Context(), Request{Profile: ProfileDeep, CheckIDs: []CheckID{CheckDurabilitySQLiteRestore}}, deps, DeepDependencies{
		Archives:      &fakeDeepArchiveReader{body: []byte("x")},
		VerifyArchive: &fakeDeepVerifier{result: DeepArchiveResult{CompressedBytes: 1, DecompressedBytes: 2, QuickCheck: "ok", ForeignKeysObserved: true, QuickCheckObserved: true, SchemaCompatibility: "current_compatible", SchemaObserved: true, MigrationCompatibility: "current_compatible", MigrationObserved: true}},
		NewTemp: func() (*vaultfs.PrivateTemp, error) {
			tmp, createErr := vaultfs.NewPrivateTemp(base)
			if tmp != nil {
				generated = tmp.Dir()
			}
			return tmp, createErr
		},
		CleanupTemp:          failingDeepCleanup,
		RecordCleanupFailure: func(path string) { cleanupFailures = append(cleanupFailures, path) },
		FreeSpace:            func(*vaultfs.PrivateTemp) (uint64, error) { return uint64(DefaultDeepLimits().MaxTempBytes), nil },
		Limits:               DefaultDeepLimits(),
	})
	if err != nil {
		t.Fatal(err)
	}
	check := report.Checks[0]
	if check.Status != StatusUnknown || check.Confidence != ConfidenceUnknown || check.Evidence["cleanup_complete"] != false {
		t.Fatalf("cleanup-failed restore check = %#v", check)
	}
	if got := cleanupFailures; !reflect.DeepEqual(got, []string{generated}) {
		t.Fatalf("local cleanup paths = %#v, want %q", got, generated)
	}
	encoded, marshalErr := json.Marshal(report)
	if marshalErr != nil {
		t.Fatal(marshalErr)
	}
	if bytes.Contains(encoded, []byte(generated)) {
		t.Fatalf("shared report leaked cleanup path: %s", encoded)
	}
	// The injected failure intentionally left the directory behind; remove it
	// after asserting the local diagnostic path.
	if err := os.RemoveAll(generated); err != nil {
		t.Fatal(err)
	}
}

func TestRunDeepCleanupFailurePreservesVerifiedInvalidClassification(t *testing.T) {
	now := time.Date(2026, 7, 14, 12, 0, 0, 0, time.UTC)
	tests := []struct {
		name   string
		result DeepArchiveResult
	}{
		{name: "corrupt gzip", result: DeepArchiveResult{CompressedBytes: 8}},
		{name: "quick check", result: DeepArchiveResult{CompressedBytes: 8, DecompressedBytes: 16, QuickCheck: "violation", QuickCheckObserved: true, ForeignKeysObserved: true, SchemaCompatibility: "current_compatible", SchemaObserved: true, MigrationCompatibility: "current_compatible", MigrationObserved: true}},
		{name: "foreign key", result: DeepArchiveResult{CompressedBytes: 8, DecompressedBytes: 16, QuickCheck: "ok", QuickCheckObserved: true, ForeignKeyViolationCount: 1, ForeignKeysObserved: true, SchemaCompatibility: "current_compatible", SchemaObserved: true, MigrationCompatibility: "current_compatible", MigrationObserved: true}},
		{name: "schema", result: DeepArchiveResult{CompressedBytes: 8, DecompressedBytes: 16, QuickCheck: "ok", QuickCheckObserved: true, ForeignKeysObserved: true, SchemaCompatibility: "incompatible", SchemaObserved: true, MigrationCompatibility: "current_compatible", MigrationObserved: true}},
		{name: "migration", result: DeepArchiveResult{CompressedBytes: 8, DecompressedBytes: 16, QuickCheck: "ok", QuickCheckObserved: true, ForeignKeysObserved: true, SchemaCompatibility: "current_compatible", SchemaObserved: true, MigrationCompatibility: "incompatible", MigrationObserved: true}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			deps := passingDependencies(now)
			deps.Features.SQLiteBackupSchedulerEnabled = true
			deps.Features.SQLiteProviderConfigured = true
			deps.Features.SQLiteCredentialConfigured = true
			deps.Archives = fakeArchive{value: SQLiteArchiveListing{Complete: true, Objects: []ArchiveObject{{Key: "archive/db/brain-20260714T110000Z.db.gz", ValidKey: true, SizeBytes: 1, LastModified: now.Add(-time.Hour)}}}}
			base := t.TempDir()
			var generated string
			var cleanupFailures []string
			report, err := RunDeep(t.Context(), Request{Profile: ProfileDeep, CheckIDs: []CheckID{CheckDurabilitySQLiteRestore}}, deps, DeepDependencies{
				Archives:      &fakeDeepArchiveReader{body: []byte("candidate")},
				VerifyArchive: &fakeDeepVerifier{result: test.result, err: ErrDeepCandidateInvalid},
				NewTemp: func() (*vaultfs.PrivateTemp, error) {
					tmp, createErr := vaultfs.NewPrivateTemp(base)
					if tmp != nil {
						generated = tmp.Dir()
					}
					return tmp, createErr
				},
				CleanupTemp:          failingDeepCleanup,
				RecordCleanupFailure: func(path string) { cleanupFailures = append(cleanupFailures, path) },
				FreeSpace:            func(*vaultfs.PrivateTemp) (uint64, error) { return uint64(DefaultDeepLimits().MaxTempBytes), nil },
				Limits:               DefaultDeepLimits(),
			})
			if err != nil {
				t.Fatal(err)
			}
			check := report.Checks[0]
			if check.Status != StatusFail || check.Confidence != ConfidenceHigh || check.Evidence["cleanup_complete"] != false {
				t.Fatalf("verified invalid cleanup-failed restore = %#v", check)
			}
			if !reflect.DeepEqual(cleanupFailures, []string{generated}) {
				t.Fatalf("local cleanup failures = %#v, want %q", cleanupFailures, generated)
			}
			encoded, marshalErr := json.Marshal(report)
			if marshalErr != nil {
				t.Fatal(marshalErr)
			}
			if bytes.Contains(encoded, []byte(generated)) {
				t.Fatalf("shared report leaked cleanup path: %s", encoded)
			}
			if err := os.RemoveAll(generated); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestRunDeepCleanupFailureKeepsOperationalValidationUnknown(t *testing.T) {
	now := time.Date(2026, 7, 14, 12, 0, 0, 0, time.UTC)
	deps := passingDependencies(now)
	deps.Features.SQLiteBackupSchedulerEnabled = true
	deps.Features.SQLiteProviderConfigured = true
	deps.Features.SQLiteCredentialConfigured = true
	deps.Archives = fakeArchive{value: SQLiteArchiveListing{Complete: true, Objects: []ArchiveObject{{Key: "archive/db/brain-20260714T110000Z.db.gz", ValidKey: true, SizeBytes: 1, LastModified: now.Add(-time.Hour)}}}}
	base := t.TempDir()
	var generated string
	report, err := RunDeep(t.Context(), Request{Profile: ProfileDeep, CheckIDs: []CheckID{CheckDurabilitySQLiteRestore}}, deps, DeepDependencies{
		Archives:      &fakeDeepArchiveReader{body: []byte("candidate")},
		VerifyArchive: &fakeDeepVerifier{result: DeepArchiveResult{CompressedBytes: 8, DecompressedBytes: 16}, err: errors.New("validator I/O unavailable")},
		NewTemp: func() (*vaultfs.PrivateTemp, error) {
			tmp, createErr := vaultfs.NewPrivateTemp(base)
			if tmp != nil {
				generated = tmp.Dir()
			}
			return tmp, createErr
		},
		CleanupTemp: failingDeepCleanup,
		FreeSpace:   func(*vaultfs.PrivateTemp) (uint64, error) { return uint64(DefaultDeepLimits().MaxTempBytes), nil }, Limits: DefaultDeepLimits(),
	})
	if err != nil {
		t.Fatal(err)
	}
	check := report.Checks[0]
	if check.Status != StatusUnknown || check.Confidence != ConfidenceUnknown || check.Evidence["cleanup_complete"] != false {
		t.Fatalf("operational cleanup-failed restore = %#v", check)
	}
	if err := os.RemoveAll(generated); err != nil {
		t.Fatal(err)
	}
}

func TestRunDeepUnknownRestoreOmitsUnobservedValidationEvidence(t *testing.T) {
	now := time.Date(2026, 7, 14, 12, 0, 0, 0, time.UTC)
	deps := passingDependencies(now)
	deps.Features.SQLiteBackupSchedulerEnabled = true
	deps.Features.SQLiteProviderConfigured = true
	deps.Features.SQLiteCredentialConfigured = true
	deps.Archives = fakeArchive{value: SQLiteArchiveListing{Complete: true, Objects: []ArchiveObject{{Key: "archive/db/brain-20260714T110000Z.db.gz", ValidKey: true, SizeBytes: 1, LastModified: now.Add(-time.Hour)}}}}
	report, err := RunDeep(t.Context(), Request{Profile: ProfileDeep, CheckIDs: []CheckID{CheckDurabilitySQLiteRestore}}, deps, DeepDependencies{
		Archives:      &fakeDeepArchiveReader{err: errors.New("remote unavailable")},
		VerifyArchive: &fakeDeepVerifier{}, NewTemp: func() (*vaultfs.PrivateTemp, error) { return vaultfs.NewPrivateTemp(t.TempDir()) },
		FreeSpace: func(*vaultfs.PrivateTemp) (uint64, error) { return uint64(DefaultDeepLimits().MaxTempBytes), nil }, Limits: DefaultDeepLimits(),
	})
	if err != nil {
		t.Fatal(err)
	}
	check := report.Checks[0]
	if check.Status != StatusUnknown || check.Confidence != ConfidenceUnknown {
		t.Fatalf("unknown restore check = %#v", check)
	}
	for _, key := range []string{"quick_check", "foreign_key_violation_count", "schema_compatibility", "migration_compatibility"} {
		if _, exists := check.Evidence[key]; exists {
			t.Fatalf("unobserved %s was fabricated: %#v", key, check.Evidence)
		}
	}
}

func TestRunDeepVerifiedPreValidationFailureOmitsUnobservedEvidence(t *testing.T) {
	now := time.Date(2026, 7, 14, 12, 0, 0, 0, time.UTC)
	deps := passingDependencies(now)
	deps.Features.SQLiteBackupSchedulerEnabled = true
	deps.Features.SQLiteProviderConfigured = true
	deps.Features.SQLiteCredentialConfigured = true
	deps.Archives = fakeArchive{value: SQLiteArchiveListing{Complete: true, Objects: []ArchiveObject{{Key: "archive/db/brain-20260714T110000Z.db.gz", ValidKey: true, SizeBytes: 1, LastModified: now.Add(-time.Hour)}}}}
	report, err := RunDeep(t.Context(), Request{Profile: ProfileDeep, CheckIDs: []CheckID{CheckDurabilitySQLiteRestore}}, deps, DeepDependencies{
		Archives:      &fakeDeepArchiveReader{body: []byte("not-gzip")},
		VerifyArchive: &fakeDeepVerifier{result: DeepArchiveResult{CompressedBytes: 8}, err: ErrDeepCandidateInvalid},
		NewTemp:       func() (*vaultfs.PrivateTemp, error) { return vaultfs.NewPrivateTemp(t.TempDir()) },
		FreeSpace:     func(*vaultfs.PrivateTemp) (uint64, error) { return uint64(DefaultDeepLimits().MaxTempBytes), nil }, Limits: DefaultDeepLimits(),
	})
	if err != nil {
		t.Fatal(err)
	}
	check := report.Checks[0]
	if check.Status != StatusFail || check.Confidence != ConfidenceHigh {
		t.Fatalf("verified invalid restore = %#v", check)
	}
	for _, key := range []string{"quick_check", "foreign_key_violation_count", "schema_compatibility", "migration_compatibility"} {
		if _, exists := check.Evidence[key]; exists {
			t.Fatalf("unobserved %s was fabricated: %#v", key, check.Evidence)
		}
	}
}

func TestRunDeepChecksFreeSpaceBeforeArchiveGetAndCleans(t *testing.T) {
	now := time.Date(2026, 7, 14, 12, 0, 0, 0, time.UTC)
	deps := passingDependencies(now)
	deps.Features.SQLiteBackupSchedulerEnabled = true
	deps.Features.SQLiteProviderConfigured = true
	deps.Features.SQLiteCredentialConfigured = true
	deps.Archives = fakeArchive{value: SQLiteArchiveListing{Complete: true, Objects: []ArchiveObject{{Key: "archive/db/brain-20260714T110000Z.db.gz", ValidKey: true, SizeBytes: 1, LastModified: now.Add(-time.Hour)}}}}
	base := t.TempDir()
	var generated string
	reader := &fakeDeepArchiveReader{body: []byte("must-not-read")}
	verifier := &fakeDeepVerifier{}
	limits := DefaultDeepLimits()
	report, err := RunDeep(t.Context(), Request{Profile: ProfileDeep, CheckIDs: []CheckID{CheckDurabilitySQLiteRestore}}, deps, DeepDependencies{
		Archives: reader, VerifyArchive: verifier,
		NewTemp: func() (*vaultfs.PrivateTemp, error) {
			tmp, createErr := vaultfs.NewPrivateTemp(base)
			if tmp != nil {
				generated = tmp.Dir()
			}
			return tmp, createErr
		},
		FreeSpace: func(*vaultfs.PrivateTemp) (uint64, error) { return uint64(limits.MaxTempBytes - 1), nil }, Limits: limits,
	})
	if err != nil {
		t.Fatal(err)
	}
	if reader.calls != 0 || verifier.calls != 0 {
		t.Fatalf("preflight allowed reader=%d verifier=%d", reader.calls, verifier.calls)
	}
	if report.Checks[0].Status != StatusUnknown || report.Checks[0].Evidence["cleanup_complete"] != true {
		t.Fatalf("restore check = %#v", report.Checks[0])
	}
	if _, statErr := os.Stat(generated); !errors.Is(statErr, os.ErrNotExist) {
		t.Fatalf("generated temp remains: %v", statErr)
	}
}

func TestRunDeepCleansGeneratedTempOnCancellation(t *testing.T) {
	now := time.Date(2026, 7, 14, 12, 0, 0, 0, time.UTC)
	deps := passingDependencies(now)
	deps.Features.SQLiteBackupSchedulerEnabled = true
	deps.Features.SQLiteProviderConfigured = true
	deps.Features.SQLiteCredentialConfigured = true
	deps.Archives = fakeArchive{value: SQLiteArchiveListing{Complete: true, Objects: []ArchiveObject{{Key: "archive/db/brain-20260714T110000Z.db.gz", ValidKey: true, SizeBytes: 1, LastModified: now.Add(-time.Hour)}}}}
	base := t.TempDir()
	var generated string
	ctx, cancel := context.WithTimeout(t.Context(), 20*time.Millisecond)
	defer cancel()
	_, err := RunDeep(ctx, Request{Profile: ProfileDeep, CheckIDs: []CheckID{CheckDurabilitySQLiteRestore}}, deps, DeepDependencies{
		Archives: &fakeDeepArchiveReader{body: []byte("x")}, VerifyArchive: cancelDeepVerifier{},
		NewTemp: func() (*vaultfs.PrivateTemp, error) {
			tmp, createErr := vaultfs.NewPrivateTemp(base)
			if tmp != nil {
				generated = tmp.Dir()
			}
			return tmp, createErr
		},
		FreeSpace: func(*vaultfs.PrivateTemp) (uint64, error) { return uint64(DefaultDeepLimits().MaxTempBytes), nil }, Limits: DefaultDeepLimits(),
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, statErr := os.Stat(generated); !errors.Is(statErr, os.ErrNotExist) {
		t.Fatalf("generated temp remains after cancellation: %v", statErr)
	}
}
