package notify

import (
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"sync"
	"testing"
)

func TestStoreMissingStateLoadsEmptyWithoutCreatingStateFile(t *testing.T) {
	logDir := t.TempDir()
	store, err := OpenStore(logDir)
	if err != nil {
		t.Fatal(err)
	}
	got, err := store.Load()
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(got, EmptyState()) {
		t.Fatalf("Load() = %#v, want empty state", got)
	}
	if _, err := os.Stat(filepath.Join(logDir, "notifications", "state.json")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("missing Load created state file: %v", err)
	}
}

func TestStoreCreatesPrivateDirectoryAndStateModes(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX modes are not available on Windows")
	}
	logDir := t.TempDir()
	store, err := OpenStore(logDir)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.Replace(EmptyState()); err != nil {
		t.Fatal(err)
	}
	for path, want := range map[string]os.FileMode{
		filepath.Join(logDir, "notifications"):               0o700,
		filepath.Join(logDir, "notifications", "state.json"): 0o600,
	} {
		info, err := os.Stat(path)
		if err != nil {
			t.Fatal(err)
		}
		if got := info.Mode().Perm(); got != want {
			t.Fatalf("%s mode = %o, want %o", path, got, want)
		}
	}
}

func TestStoreAtomicReplacementLeavesOnlyCompleteState(t *testing.T) {
	logDir := t.TempDir()
	store, err := OpenStore(logDir)
	if err != nil {
		t.Fatal(err)
	}
	first := EmptyState()
	if err := store.Replace(first); err != nil {
		t.Fatal(err)
	}
	second, _, err := Observe(first, stateFailed(FailureStoreOpen, 0), stateOptions())
	if err != nil {
		t.Fatal(err)
	}
	if err := store.Replace(second); err != nil {
		t.Fatal(err)
	}
	got, err := store.Load()
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(got, second) {
		t.Fatalf("Load() = %#v, want %#v", got, second)
	}
	entries, err := os.ReadDir(filepath.Join(logDir, "notifications"))
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 || entries[0].Name() != "state.json" {
		t.Fatalf("replacement left temporary files: %#v", entries)
	}
}

func TestStoreRejectsSymlinkState(t *testing.T) {
	logDir := t.TempDir()
	outside := filepath.Join(t.TempDir(), "outside")
	if err := os.WriteFile(outside, []byte(`{"schema":"dbrain.notifications.state.v1","incidents":{},"outbox":[]}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(logDir, "notifications"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(logDir, "notifications", "state.json")); err != nil {
		t.Skipf("symlink unsupported: %v", err)
	}
	if _, err := OpenStore(logDir); err == nil {
		t.Fatal("symlink state accepted")
	}
	data, err := os.ReadFile(outside)
	if err != nil || !strings.Contains(string(data), StateSchemaV1) {
		t.Fatalf("outside state modified: %q, %v", data, err)
	}
}

func TestDecodeStatePromotesV1LastDeliveryToPerProviderSummary(t *testing.T) {
	state, err := decodeState([]byte(`{"schema":"dbrain.notifications.state.v1","incidents":{},"outbox":[],"last_delivery":{"notification_id":"ntf_012345678901234567890123","provider":"buzz","kind":"failure","status":"accepted","at":"2026-08-04T00:00:00Z"}}`))
	if err != nil {
		t.Fatal(err)
	}
	if state.Schema != StateSchemaV2 {
		t.Fatalf("schema = %q, want %q", state.Schema, StateSchemaV2)
	}
	if state.LastDelivery != (DeliverySummary{}) {
		t.Fatalf("legacy last delivery retained: %#v", state.LastDelivery)
	}
	got, ok := state.LastDeliveries["buzz"]
	if !ok || got.Provider != "buzz" || got.Status != DeliveryAccepted || got.NotificationID != "ntf_012345678901234567890123" {
		t.Fatalf("promoted provider delivery = %#v", state.LastDeliveries)
	}
}

func TestStoreRejectsSymlinkDirectory(t *testing.T) {
	logDir := t.TempDir()
	outside := t.TempDir()
	if err := os.Symlink(outside, filepath.Join(logDir, "notifications")); err != nil {
		t.Skipf("symlink unsupported: %v", err)
	}
	if _, err := OpenStore(logDir); err == nil {
		t.Fatal("symlink notification directory accepted")
	}
	entries, err := os.ReadDir(outside)
	if err != nil || len(entries) != 0 {
		t.Fatalf("outside directory modified: %#v, %v", entries, err)
	}
}

func TestStoreRejectsNonRegularState(t *testing.T) {
	logDir := t.TempDir()
	statePath := filepath.Join(logDir, "notifications", "state.json")
	if err := os.MkdirAll(statePath, 0o700); err != nil {
		t.Fatal(err)
	}
	if _, err := OpenStore(logDir); err == nil {
		t.Fatal("non-regular state accepted")
	}
}

func TestStoreRejectsUnknownFieldsTrailingJSONAndInvalidSchema(t *testing.T) {
	for _, test := range []struct {
		name string
		body string
	}{
		{name: "unknown field", body: `{"schema":"dbrain.notifications.state.v1","incidents":{},"outbox":[],"body":"secret"}`},
		{name: "trailing JSON", body: `{"schema":"dbrain.notifications.state.v1","incidents":{},"outbox":[]} {}`},
		{name: "invalid schema", body: `{"schema":"dbrain.notifications.state.v3","incidents":{},"outbox":[]}`},
	} {
		t.Run(test.name, func(t *testing.T) {
			logDir := t.TempDir()
			stateDir := filepath.Join(logDir, "notifications")
			if err := os.Mkdir(stateDir, 0o700); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(stateDir, "state.json"), []byte(test.body), 0o600); err != nil {
				t.Fatal(err)
			}
			store, err := OpenStore(logDir)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := store.Load(); err == nil {
				t.Fatalf("%s accepted", test.name)
			}
		})
	}
}

func TestStoreRejectsStateAboveByteLimit(t *testing.T) {
	logDir := t.TempDir()
	stateDir := filepath.Join(logDir, "notifications")
	if err := os.Mkdir(stateDir, 0o700); err != nil {
		t.Fatal(err)
	}
	body := append([]byte(`{"schema":"dbrain.notifications.state.v1","incidents":{},"outbox":[],"padding":"`), make([]byte, maxStateBytes)...)
	body = append(body, []byte(`"}`)...)
	if err := os.WriteFile(filepath.Join(stateDir, "state.json"), body, 0o600); err != nil {
		t.Fatal(err)
	}
	store, err := OpenStore(logDir)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.Load(); err == nil {
		t.Fatal("oversized state accepted")
	}
}

func TestStoreInterruptedReplacementPreservesOldState(t *testing.T) {
	wantErr := errors.New("interrupted temporary write")
	fs := &recordingStateFS{state: EmptyState(), replaceErr: wantErr}
	store := &Store{fs: fs}
	next, _, err := Observe(EmptyState(), stateFailed(FailureStoreOpen, 0), stateOptions())
	if err != nil {
		t.Fatal(err)
	}
	if err := store.Replace(next); !errors.Is(err, wantErr) {
		t.Fatalf("Replace error = %v, want %v", err, wantErr)
	}
	got, err := store.Load()
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(got, EmptyState()) {
		t.Fatalf("interrupted replacement changed state: %#v", got)
	}
}

func TestStoreFailedTemporaryFileSyncCleansUpAndPreservesOldState(t *testing.T) {
	logDir := t.TempDir()
	store, err := OpenStore(logDir)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.Replace(EmptyState()); err != nil {
		t.Fatal(err)
	}
	wantErr := errors.New("injected file sync interruption")
	originalSync := syncNotificationStateFile
	syncNotificationStateFile = func(*os.File) error { return wantErr }
	t.Cleanup(func() { syncNotificationStateFile = originalSync })
	next, _, err := Observe(EmptyState(), stateFailed(FailureStoreOpen, 0), stateOptions())
	if err != nil {
		t.Fatal(err)
	}
	if err := store.Replace(next); !errors.Is(err, wantErr) {
		t.Fatalf("Replace error = %v, want %v", err, wantErr)
	}
	got, err := store.Load()
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(got, EmptyState()) {
		t.Fatalf("failed temp sync changed state: %#v", got)
	}
	entries, err := os.ReadDir(filepath.Join(logDir, "notifications"))
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 || entries[0].Name() != "state.json" {
		t.Fatalf("failed temp sync left files: %#v", entries)
	}
}

func TestStoreReplacementSyncsContainingDirectory(t *testing.T) {
	store, err := OpenStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	originalSync := syncNotificationStateDirectory
	var calls int
	syncNotificationStateDirectory = func(sync func() error) error {
		calls++
		return sync()
	}
	t.Cleanup(func() { syncNotificationStateDirectory = originalSync })
	if err := store.Replace(EmptyState()); err != nil {
		t.Fatal(err)
	}
	if calls != 1 {
		t.Fatalf("directory sync calls = %d, want 1", calls)
	}
}

func TestStoreReaderIsNoCreateAndSeesLaterState(t *testing.T) {
	logDir := t.TempDir()
	if runtime.GOOS != "windows" {
		if err := os.Chmod(logDir, 0o750); err != nil {
			t.Fatal(err)
		}
	}
	reader, err := OpenReader(logDir)
	if err != nil {
		t.Fatal(err)
	}
	got, err := reader.Load()
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(got, EmptyState()) {
		t.Fatalf("missing reader state = %#v", got)
	}
	if _, err := os.Stat(filepath.Join(logDir, "notifications")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("reader created notification directory: %v", err)
	}
	if runtime.GOOS != "windows" {
		info, err := os.Stat(logDir)
		if err != nil {
			t.Fatal(err)
		}
		if got := info.Mode().Perm(); got != 0o750 {
			t.Fatalf("reader changed log directory mode to %o", got)
		}
	}
	store, err := OpenStore(logDir)
	if err != nil {
		t.Fatal(err)
	}
	next, _, err := Observe(EmptyState(), stateFailed(FailureStoreOpen, 0), stateOptions())
	if err != nil {
		t.Fatal(err)
	}
	if err := store.Replace(next); err != nil {
		t.Fatal(err)
	}
	got, err = reader.Load()
	if err != nil || !reflect.DeepEqual(got, next) {
		t.Fatalf("reader after replace = %#v, %v", got, err)
	}
}

func TestStoreReaderRejectsSymlinkStateWithoutMutation(t *testing.T) {
	logDir := t.TempDir()
	stateDir := filepath.Join(logDir, "notifications")
	if err := os.Mkdir(stateDir, 0o700); err != nil {
		t.Fatal(err)
	}
	outside := filepath.Join(t.TempDir(), "outside")
	want := []byte(`{"schema":"dbrain.notifications.state.v1","incidents":{},"outbox":[]}`)
	if err := os.WriteFile(outside, want, 0o640); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(stateDir, "state.json")); err != nil {
		t.Skipf("symlink unsupported: %v", err)
	}
	reader, err := OpenReader(logDir)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := reader.Load(); err == nil {
		t.Fatal("read-only state reader followed symlink")
	}
	data, err := os.ReadFile(outside)
	if err != nil || !reflect.DeepEqual(data, want) {
		t.Fatalf("read-only state reader mutated target: %q, %v", data, err)
	}
	if runtime.GOOS != "windows" {
		info, err := os.Stat(outside)
		if err != nil {
			t.Fatal(err)
		}
		if got := info.Mode().Perm(); got != 0o640 {
			t.Fatalf("read-only state reader changed target mode to %o", got)
		}
	}
}

func TestStoreSerializesConcurrentLoadAndReplace(t *testing.T) {
	store, err := OpenStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	states := []State{EmptyState()}
	next, _, err := Observe(EmptyState(), stateFailed(FailureStoreOpen, 0), stateOptions())
	if err != nil {
		t.Fatal(err)
	}
	states = append(states, next)
	var wg sync.WaitGroup
	for index := 0; index < 40; index++ {
		wg.Add(1)
		go func(index int) {
			defer wg.Done()
			if err := store.Replace(states[index%len(states)]); err != nil {
				t.Errorf("Replace: %v", err)
				return
			}
			if _, err := store.Load(); err != nil {
				t.Errorf("Load: %v", err)
			}
		}(index)
	}
	wg.Wait()
	if _, err := store.Load(); err != nil {
		t.Fatalf("final Load: %v", err)
	}
}

type recordingStateFS struct {
	mu         sync.Mutex
	state      State
	replaceErr error
}

func (f *recordingStateFS) ReadState(int64) ([]byte, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	data, err := encodeState(f.state)
	return data, err
}

func (f *recordingStateFS) ReplaceState(data []byte) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.replaceErr != nil {
		return f.replaceErr
	}
	state, err := decodeState(data)
	if err != nil {
		return err
	}
	f.state = state
	return nil
}
