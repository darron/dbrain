package audit

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

type retentionTestFS struct {
	files     []reportFileInfo
	removed   []string
	removeErr map[string]error
}

func (*retentionTestFS) AppendReport(string, []byte) error        { return nil }
func (*retentionTestFS) ReadReport(string, int64) ([]byte, error) { return nil, os.ErrNotExist }
func (f *retentionTestFS) ListReports() ([]reportFileInfo, error) {
	return append([]reportFileInfo(nil), f.files...), nil
}
func (f *retentionTestFS) RemoveReport(name string) error {
	if err := f.removeErr[name]; err != nil {
		return err
	}
	f.removed = append(f.removed, name)
	return nil
}
func (*retentionTestFS) ReadState(int64) ([]byte, error) { return nil, os.ErrNotExist }
func (*retentionTestFS) ReplaceState([]byte) error       { return nil }

type historyReadTestFS struct {
	files   []reportFileInfo
	data    map[string][]byte
	readErr map[string]error
}

func (*historyReadTestFS) AppendReport(string, []byte) error { return nil }
func (f *historyReadTestFS) ReadReport(name string, _ int64) ([]byte, error) {
	if err := f.readErr[name]; err != nil {
		return nil, err
	}
	return append([]byte(nil), f.data[name]...), nil
}
func (f *historyReadTestFS) ListReports() ([]reportFileInfo, error) {
	return append([]reportFileInfo(nil), f.files...), nil
}
func (*historyReadTestFS) RemoveReport(string) error       { return nil }
func (*historyReadTestFS) ReadState(int64) ([]byte, error) { return nil, os.ErrNotExist }
func (*historyReadTestFS) ReplaceState([]byte) error       { return nil }

func testStoredReport(t *testing.T, profile Profile, completed time.Time, status Status) Report {
	t.Helper()
	started := completed.Add(-time.Second)
	report := NewReport(profile, started)
	report.AuditID = "audit_" + started.UTC().Format("20060102T150405.000000000Z") + "_00000001"
	report.Scope = Scope{Categories: []Category{CategoryBoundary}, Sources: []Source{}, CheckIDs: []CheckID{CheckBoundaryConfig}, Filtered: true}
	report.Boundary.Layout = "xdg"
	report.CompletedAt = completed.UTC()
	check := Check{ID: CheckBoundaryConfig, Category: CategoryBoundary, Status: status, Confidence: ConfidenceHigh, Required: true, Summary: fixedSummary(CheckBoundaryConfig), ObservedAt: completed.UTC(), Evidence: Evidence{"layout": "xdg", "config_source": "default", "verified": true}}
	if status == StatusUnknown {
		check.Confidence = ConfidenceUnknown
		check.ErrorCode = ErrorUnavailable
		check.Evidence = Evidence{}
	}
	report.Checks = []Check{check}
	FinalizeReport(&report)
	if err := ValidateReport(report); err != nil {
		t.Fatalf("test report: %v", err)
	}
	return report
}

func TestReportStoreUsesPrivateFixedDailyPathsAndExactProfiles(t *testing.T) {
	logDir := t.TempDir()
	store, err := NewReportStore(logDir)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 7, 14, 1, 2, 3, 0, time.UTC)
	fast := testStoredReport(t, ProfileFast, now, StatusPass)
	standard := testStoredReport(t, ProfileStandard, now.Add(time.Minute), StatusUnknown)
	if err := store.Save(fast); err != nil {
		t.Fatal(err)
	}
	if err := store.Save(standard); err != nil {
		t.Fatal(err)
	}

	for _, dir := range []string{filepath.Join(logDir, "audit"), filepath.Join(logDir, "audit", "reports")} {
		info, err := os.Stat(dir)
		if err != nil {
			t.Fatal(err)
		}
		if info.Mode().Perm() != 0o700 {
			t.Fatalf("%s mode = %o", dir, info.Mode().Perm())
		}
	}
	reportPath := filepath.Join(logDir, "audit", "reports", "2026-07-14.jsonl")
	info, err := os.Stat(reportPath)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Fatalf("report mode = %o", info.Mode().Perm())
	}

	gotFast, err := store.Latest(ProfileFast)
	if err != nil || gotFast == nil || gotFast.AuditID != fast.AuditID {
		t.Fatalf("Latest(fast) = %#v, %v", gotFast, err)
	}
	gotStandard, err := store.History(ProfileStandard, 10)
	if err != nil || len(gotStandard) != 1 || gotStandard[0].AuditID != standard.AuditID {
		t.Fatalf("History(standard) = %#v, %v", gotStandard, err)
	}
	missing, err := store.Latest(ProfileDeep)
	if err != nil || missing != nil {
		t.Fatalf("Latest(deep) = %#v, %v", missing, err)
	}
}

func TestReportStoreRotatesUTCAndIsolatesMalformedLines(t *testing.T) {
	logDir := t.TempDir()
	store, err := NewReportStore(logDir)
	if err != nil {
		t.Fatal(err)
	}
	first := testStoredReport(t, ProfileFast, time.Date(2026, 7, 14, 23, 59, 59, 0, time.UTC), StatusPass)
	second := testStoredReport(t, ProfileFast, time.Date(2026, 7, 15, 0, 0, 1, 0, time.UTC), StatusUnknown)
	if err := store.Save(first); err != nil {
		t.Fatal(err)
	}
	if err := store.Save(second); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(logDir, "audit", "reports", "2026-07-15.jsonl")
	file, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := file.WriteString("{malformed\n"); err != nil {
		t.Fatal(err)
	}
	_ = file.Close()

	history, err := store.History(ProfileFast, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(history) != 2 || history[0].AuditID != second.AuditID || history[1].AuditID != first.AuditID {
		t.Fatalf("history = %#v", history)
	}
	if _, err := os.Stat(filepath.Join(logDir, "audit", "reports", "2026-07-14.jsonl")); err != nil {
		t.Fatal(err)
	}
}

func TestReportStoreSeparatesUnterminatedTailBeforeSavingValidReport(t *testing.T) {
	logDir := t.TempDir()
	store, err := NewReportStore(logDir)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 7, 14, 5, 0, 0, 0, time.UTC)
	path := filepath.Join(logDir, "audit", "reports", "2026-07-14.jsonl")
	if err := os.WriteFile(path, []byte(`{"crash_truncated":`), 0o600); err != nil {
		t.Fatal(err)
	}
	want := testStoredReport(t, ProfileFast, now, StatusPass)
	if err := store.Save(want); err != nil {
		t.Fatal(err)
	}
	history, err := store.History(ProfileFast, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(history) != 1 || history[0].AuditID != want.AuditID {
		t.Fatalf("history after truncated tail = %#v", history)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), "crash_truncated\":\n{") {
		t.Fatalf("truncated tail was not isolated before valid JSON: %q", data)
	}
}

func TestReportStoreHistoryPropagatesFileReadFailure(t *testing.T) {
	now := time.Date(2026, 7, 14, 6, 0, 0, 0, time.UTC)
	older := testStoredReport(t, ProfileFast, now.Add(-24*time.Hour), StatusPass)
	olderData, err := json.Marshal(older)
	if err != nil {
		t.Fatal(err)
	}
	wantErr := errors.New("injected report confinement failure")
	fs := &historyReadTestFS{
		files:   []reportFileInfo{{Name: "2026-07-14.jsonl", Size: 10}, {Name: "2026-07-13.jsonl", Size: int64(len(olderData) + 1)}},
		data:    map[string][]byte{"2026-07-13.jsonl": append(olderData, '\n')},
		readErr: map[string]error{"2026-07-14.jsonl": wantErr},
	}
	store := &ReportStore{fs: fs, now: func() time.Time { return now }}
	history, err := store.History(ProfileFast, 10)
	if !errors.Is(err, wantErr) || history != nil {
		t.Fatalf("History silently fell back to older data: history=%#v err=%v", history, err)
	}
}

func TestReportReaderSkipsReportRemovedByConcurrentRetention(t *testing.T) {
	now := time.Date(2026, 7, 14, 6, 0, 0, 0, time.UTC)
	older := testStoredReport(t, ProfileStandard, now.Add(-24*time.Hour), StatusPass)
	olderData, err := json.Marshal(older)
	if err != nil {
		t.Fatal(err)
	}
	fs := &historyReadTestFS{
		files:   []reportFileInfo{{Name: "2026-07-14.jsonl", Size: 10}, {Name: "2026-07-13.jsonl", Size: int64(len(olderData) + 1)}},
		data:    map[string][]byte{"2026-07-13.jsonl": append(olderData, '\n')},
		readErr: map[string]error{"2026-07-14.jsonl": os.ErrNotExist},
	}
	reader := &ReportReader{fs: fs}
	latest, err := reader.Latest(ProfileStandard)
	if err != nil {
		t.Fatalf("Latest after concurrent retention: %v", err)
	}
	if latest == nil || latest.AuditID != older.AuditID {
		t.Fatalf("Latest after concurrent retention = %#v", latest)
	}
}

func TestReportStoreHistoryPropagatesMatchingGeneratedNameConfinementFailure(t *testing.T) {
	logDir := t.TempDir()
	store, err := NewReportStore(logDir)
	if err != nil {
		t.Fatal(err)
	}
	outside := filepath.Join(t.TempDir(), "outside.jsonl")
	if err := os.WriteFile(outside, []byte("outside"), 0o600); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(logDir, "audit", "reports", "2026-07-14.jsonl")
	if err := os.Symlink(outside, link); err != nil {
		t.Skipf("symlink unsupported: %v", err)
	}
	history, err := store.History(ProfileFast, 1)
	if err == nil || history != nil {
		t.Fatalf("matching generated-name symlink disappeared: history=%#v err=%v", history, err)
	}
}

func TestReportStoreSkipsTrailingGarbageAndRepairsPrivateModes(t *testing.T) {
	logDir := t.TempDir()
	store, err := NewReportStore(logDir)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 7, 14, 4, 0, 0, 0, time.UTC)
	valid := testStoredReport(t, ProfileFast, now, StatusPass)
	if err := store.Save(valid); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(logDir, "audit", "reports", "2026-07-14.jsonl")
	data, _ := json.Marshal(valid)
	file, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0)
	if err != nil {
		t.Fatal(err)
	}
	_, _ = file.Write(append(append([]byte{}, data...), []byte(" trailing\n")...))
	_ = file.Close()
	if err := os.Chmod(path, 0o644); err != nil {
		t.Fatal(err)
	}
	history, err := store.History(ProfileFast, 10)
	if err != nil || len(history) != 1 {
		t.Fatalf("history = %#v, %v", history, err)
	}
	info, _ := os.Stat(path)
	if info.Mode().Perm() != 0o600 {
		t.Fatalf("historical report mode = %o", info.Mode().Perm())
	}

	if err := store.SaveAlertState(emptyAlertState()); err != nil {
		t.Fatal(err)
	}
	statePath := filepath.Join(logDir, "audit", "alert-state.json")
	if err := os.Chmod(statePath, 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := store.LoadAlertState(); err != nil {
		t.Fatal(err)
	}
	info, _ = os.Stat(statePath)
	if info.Mode().Perm() != 0o600 {
		t.Fatalf("alert state mode = %o", info.Mode().Perm())
	}
	if err := os.WriteFile(statePath, []byte(`{"schema":"dbrain.audit.alert-state.v1","profiles":{}} {}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := store.LoadAlertState(); err == nil {
		t.Fatal("alert state with trailing JSON accepted")
	}
}

func TestReportStoreRejectsSymlinksAndPrivateContent(t *testing.T) {
	logDir := t.TempDir()
	outside := t.TempDir()
	if err := os.Symlink(outside, filepath.Join(logDir, "audit")); err != nil {
		t.Skipf("symlink unsupported: %v", err)
	}
	if _, err := NewReportStore(logDir); err == nil {
		t.Fatal("expected symlink rejection")
	}
	if entries, err := os.ReadDir(outside); err != nil || len(entries) != 0 {
		t.Fatalf("outside modified: %v, %v", entries, err)
	}

	cleanLog := t.TempDir()
	store, err := NewReportStore(cleanLog)
	if err != nil {
		t.Fatal(err)
	}
	report := testStoredReport(t, ProfileFast, time.Now().UTC(), StatusPass)
	report.Boundary.Version = "/Users/private/brain.db"
	if err := store.Save(report); err == nil {
		t.Fatal("expected private/path-bearing report rejection")
	}
	if entries, err := os.ReadDir(filepath.Join(cleanLog, "audit", "reports")); err != nil || len(entries) != 0 {
		t.Fatalf("invalid report persisted: %v, %v", entries, err)
	}
}

func TestReportStoreAtomicallyPersistsContentFreeAlertState(t *testing.T) {
	store, err := NewReportStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 7, 14, 2, 0, 0, 0, time.UTC)
	state := AlertState{Schema: AlertStateSchemaV1, Profiles: map[Profile]ProfileAlertState{
		ProfileFast: {Checks: map[CheckID]CheckAlertState{CheckBoundaryConfig: {Confirmed: StatusWarn, LastObserved: StatusWarn, LastNotifiedAt: now}}},
	}}
	if err := store.SaveAlertState(state); err != nil {
		t.Fatal(err)
	}
	loaded, err := store.LoadAlertState()
	if err != nil {
		t.Fatal(err)
	}
	data, _ := json.Marshal(loaded)
	if strings.Contains(string(data), "secret") || loaded.Profiles[ProfileFast].Checks[CheckBoundaryConfig].Confirmed != StatusWarn {
		t.Fatalf("loaded state = %s", data)
	}
}

func TestReportRetentionEnforcesAgeAndByteCapOldestFirst(t *testing.T) {
	now := time.Date(2026, 7, 14, 12, 0, 0, 0, time.UTC)
	fs := &retentionTestFS{files: []reportFileInfo{
		{Name: "2026-01-01.jsonl", Size: 1},
		{Name: "2026-07-10.jsonl", Size: 140 << 20},
		{Name: "2026-07-11.jsonl", Size: 140 << 20},
		{Name: "2026-07-14.jsonl", Size: 1},
		{Name: "unrelated.txt", Size: 1 << 40},
	}}
	store := &ReportStore{fs: fs, now: func() time.Time { return now }}
	if err := store.enforceRetentionLocked(); err != nil {
		t.Fatal(err)
	}
	want := []string{"2026-01-01.jsonl", "2026-07-10.jsonl"}
	if len(fs.removed) != len(want) || fs.removed[0] != want[0] || fs.removed[1] != want[1] {
		t.Fatalf("removed = %#v, want %#v", fs.removed, want)
	}
}

func TestReportRetentionPropagatesRemovalFailure(t *testing.T) {
	wantErr := errors.New("injected retention failure")
	fs := &retentionTestFS{files: []reportFileInfo{{Name: "2020-01-01.jsonl", Size: 1}}, removeErr: map[string]error{"2020-01-01.jsonl": wantErr}}
	store := &ReportStore{fs: fs, now: func() time.Time { return time.Date(2026, 7, 14, 0, 0, 0, 0, time.UTC) }}
	if err := store.enforceRetentionLocked(); !errors.Is(err, wantErr) {
		t.Fatalf("retention error = %v", err)
	}
}

func TestReportRetentionKeepsTheWholeCutoffUTCDay(t *testing.T) {
	now := time.Date(2026, 7, 14, 23, 59, 59, 0, time.UTC)
	cutoffDay := now.AddDate(0, 0, -reportRetentionDays).Format("2006-01-02") + ".jsonl"
	fs := &retentionTestFS{files: []reportFileInfo{{Name: cutoffDay, Size: 1}}}
	store := &ReportStore{fs: fs, now: func() time.Time { return now }}
	if err := store.enforceRetentionLocked(); err != nil {
		t.Fatal(err)
	}
	if len(fs.removed) != 0 {
		t.Fatalf("cutoff UTC day removed: %#v", fs.removed)
	}
}

func TestReportReaderMissingTreeIsNotFoundWithoutFilesystemMutation(t *testing.T) {
	logDir := t.TempDir()
	if runtime.GOOS != "windows" {
		if err := os.Chmod(logDir, 0o750); err != nil {
			t.Fatalf("chmod log dir: %v", err)
		}
	}
	reader, err := NewReportReader(logDir)
	if err != nil {
		t.Fatalf("NewReportReader: %v", err)
	}
	report, err := reader.Latest(ProfileStandard)
	if err != nil {
		t.Fatalf("Latest missing: %v", err)
	}
	if report != nil {
		t.Fatalf("Latest missing = %#v, want nil", report)
	}
	if _, err := os.Stat(filepath.Join(logDir, "audit")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("read-only report reader created audit tree: %v", err)
	}
	info, err := os.Stat(logDir)
	if err != nil {
		t.Fatalf("stat log dir: %v", err)
	}
	if got := info.Mode().Perm(); runtime.GOOS != "windows" && got != 0o750 {
		t.Fatalf("read-only report reader changed log dir mode to %o", got)
	}
}

func TestReportReaderOpenedBeforeWriterSeesLaterReports(t *testing.T) {
	logDir := t.TempDir()
	reader, err := NewReportReader(logDir)
	if err != nil {
		t.Fatalf("NewReportReader: %v", err)
	}
	if report, err := reader.Latest(ProfileStandard); err != nil || report != nil {
		t.Fatalf("initial Latest = %#v, %v", report, err)
	}
	writer, err := NewReportStore(logDir)
	if err != nil {
		t.Fatalf("NewReportStore: %v", err)
	}
	report := testStoredReport(t, ProfileStandard, time.Date(2026, 7, 14, 12, 0, 0, 0, time.UTC), StatusPass)
	if err := writer.Save(report); err != nil {
		t.Fatalf("Save: %v", err)
	}
	latest, err := reader.Latest(ProfileStandard)
	if err != nil {
		t.Fatalf("Latest after save: %v", err)
	}
	if latest == nil || latest.AuditID != report.AuditID {
		t.Fatalf("Latest after save = %#v", latest)
	}
}
