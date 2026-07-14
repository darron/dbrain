package sqlitearchive

import (
	"context"
	"io"
	"time"
)

const (
	DefaultPrefix = "archive/db"

	sqliteDriverName = "sqlite"
	timestampLayout  = "20060102T150405Z"
)

type Options struct {
	Prefix         string
	Now            func() time.Time
	Store          ObjectStore
	Writer         ObjectWriter
	OperationLease *OperationLease
	Progress       func(Event)
}

type ObjectWriter interface {
	PutObject(ctx context.Context, key string, body io.Reader, contentType string, contentLength int64) (string, error)
}

type ObjectLister interface {
	ListObjects(ctx context.Context, prefix string) ([]Object, error)
}

type ObjectReader interface {
	GetObject(ctx context.Context, key string) (io.ReadCloser, error)
}

type ObjectStore interface {
	ObjectWriter
	ObjectLister
	ObjectReader
}

type Object struct {
	Key          string    `json:"key"`
	LastModified time.Time `json:"last_modified"`
	Size         int64     `json:"size"`
}

type ArchiveResult struct {
	Key          string    `json:"key"`
	LocalDBPath  string    `json:"local_db_path"`
	SnapshotSize int64     `json:"snapshot_size"`
	ArchiveSize  int64     `json:"archive_size"`
	ETag         string    `json:"etag,omitempty"`
	ArchivedAt   time.Time `json:"archived_at"`
}

type RestorePlan struct {
	Object Object `json:"object"`
}

type RestoreResult struct {
	Key          string    `json:"key"`
	RestoredPath string    `json:"restored_path"`
	BackupPaths  []string  `json:"backup_paths,omitempty"`
	RestoredAt   time.Time `json:"restored_at"`
}

type EventKind string

const (
	EventStageStart       EventKind = "stage_start"
	EventStageDone        EventKind = "stage_done"
	EventTransferProgress EventKind = "transfer_progress"
)

type Event struct {
	Kind    EventKind
	Stage   string
	Message string
	Current int64
	Total   int64
}
