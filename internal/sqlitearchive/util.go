package sqlitearchive

import (
	"fmt"
	"io"
	"path"
	"path/filepath"
	"strings"
	"time"
)

func requireStore(opts Options) (ObjectStore, error) {
	if opts.Store == nil {
		return nil, fmt.Errorf("object store is required")
	}
	return opts.Store, nil
}

func optionNow(opts Options) time.Time {
	if opts.Now != nil {
		return opts.Now()
	}
	return time.Now()
}

func objectKey(prefix string, name string) string {
	prefix = normalizePrefix(prefix)
	name = strings.TrimLeft(filepath.ToSlash(strings.TrimSpace(name)), "/")
	if prefix == "" {
		return name
	}
	return prefix + "/" + name
}

func normalizePrefix(prefix string) string {
	prefix = filepath.ToSlash(strings.TrimSpace(prefix))
	return strings.Trim(prefix, "/")
}

func effectivePrefix(prefix string) string {
	prefix = normalizePrefix(prefix)
	if prefix == "" {
		return DefaultPrefix
	}
	return prefix
}

func isSQLiteArchiveKey(key string, prefix string) bool {
	key = filepath.ToSlash(strings.TrimSpace(key))
	prefix = normalizePrefix(prefix)
	if prefix != "" && path.Dir(key) != prefix {
		return false
	}
	base := path.Base(key)
	const start, end = "brain-", ".db.gz"
	if !strings.HasPrefix(base, start) || !strings.HasSuffix(base, end) {
		return false
	}
	timestamp := strings.TrimSuffix(strings.TrimPrefix(base, start), end)
	_, err := time.Parse(timestampLayout, timestamp)
	return err == nil
}

// IsSQLiteArchiveKey reports whether key has the canonical SQLite archive name
// under prefix. Audit adapters use the same predicate as restore selection.
func IsSQLiteArchiveKey(key string, prefix string) bool {
	return isSQLiteArchiveKey(key, normalizePrefix(prefix))
}

func objectNewer(a Object, b Object) bool {
	if !a.LastModified.Equal(b.LastModified) {
		return a.LastModified.After(b.LastModified)
	}
	return a.Key > b.Key
}

type progressReader struct {
	reader  io.Reader
	current int64
	total   int64
	onRead  func(current int64, total int64)
}

func (r *progressReader) Read(p []byte) (int, error) {
	n, err := r.reader.Read(p)
	if n > 0 {
		r.current += int64(n)
		if r.onRead != nil {
			r.onRead(r.current, r.total)
		}
	}
	return n, err
}

func emitProgress(opts Options, event Event) {
	if opts.Progress != nil {
		opts.Progress(event)
	}
}
