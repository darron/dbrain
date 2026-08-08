package bskyapi

import (
	"path/filepath"
	"testing"

	"github.com/syndtr/goleveldb/leveldb"
)

func TestIsBSKYStorageKeyMatchesOnlyBlueskyOriginAndEntry(t *testing.T) {
	tests := []struct {
		name string
		key  string
		want bool
	}{
		{name: "chromium prefixed origin", key: "_https://bsky.app\x00BSKY_STORAGE", want: true},
		{name: "chromium prefixed origin with marker", key: "_https://bsky.app\x00\x01BSKY_STORAGE", want: true},
		{name: "unprefixed origin", key: "https://bsky.app\x00BSKY_STORAGE", want: true},
		{name: "unprefixed origin with slash", key: "https://bsky.app/\x00BSKY_STORAGE", want: true},
		{name: "wrong entry", key: "_https://bsky.app\x00other", want: false},
		{name: "wrong origin", key: "_https://evil.example\x00BSKY_STORAGE", want: false},
		{name: "entry suffix", key: "_https://bsky.app\x00BSKY_STORAGE_EXTRA", want: false},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := isBSKYStorageKey(test.key); got != test.want {
				t.Fatalf("isBSKYStorageKey(%q) = %t, want %t", test.key, got, test.want)
			}
		})
	}
}

func TestSnapshotLevelDBAndReadBSKYStorage(t *testing.T) {
	source := filepath.Join(t.TempDir(), "leveldb")
	db, err := leveldb.OpenFile(source, nil)
	if err != nil {
		t.Fatalf("open source LevelDB: %v", err)
	}
	state := []byte(`{"session":{"accounts":[{"did":"did:plc:one"}]}}`)
	if err := db.Put([]byte("_https://bsky.app\x00BSKY_STORAGE"), state, nil); err != nil {
		t.Fatalf("put BSKY_STORAGE: %v", err)
	}
	if err := db.Put([]byte("_https://bsky.app\x00other"), []byte("ignored"), nil); err != nil {
		t.Fatalf("put unrelated entry: %v", err)
	}
	if err := db.Close(); err != nil {
		t.Fatalf("close source LevelDB: %v", err)
	}

	snapshot, err := snapshotLevelDB(source)
	if err != nil {
		t.Fatalf("snapshotLevelDB: %v", err)
	}
	defer removeSnapshot(snapshot)

	got, err := readBSKYStorageFromLevelDB(snapshot)
	if err != nil {
		t.Fatalf("readBSKYStorageFromLevelDB: %v", err)
	}
	if string(got) != string(state) {
		t.Fatalf("state = %q, want %q", got, state)
	}
}

func TestReadBSKYStorageStripsChromeValueMarker(t *testing.T) {
	source := filepath.Join(t.TempDir(), "leveldb")
	db, err := leveldb.OpenFile(source, nil)
	if err != nil {
		t.Fatalf("open source LevelDB: %v", err)
	}
	state := []byte(`{"session":{"accounts":[{"did":"did:plc:one"}]}}`)
	marked := append([]byte{0x01}, state...)
	if err := db.Put([]byte("_https://bsky.app\x00\x01BSKY_STORAGE"), marked, nil); err != nil {
		t.Fatalf("put marked BSKY_STORAGE: %v", err)
	}
	if err := db.Close(); err != nil {
		t.Fatalf("close source LevelDB: %v", err)
	}

	snapshot, err := snapshotLevelDB(source)
	if err != nil {
		t.Fatalf("snapshotLevelDB: %v", err)
	}
	defer removeSnapshot(snapshot)

	got, err := readBSKYStorageFromLevelDB(snapshot)
	if err != nil {
		t.Fatalf("readBSKYStorageFromLevelDB: %v", err)
	}
	if string(got) != string(state) {
		t.Fatalf("state = %q, want %q", got, state)
	}
}
