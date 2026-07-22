//go:build usearch && cgo

package semanticindex

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"

	"github.com/darron/dbrain/internal/semanticsegment"
)

// USearchRoot is a verified, closeable native view of one immutable root. It
// is intentionally not a serving searcher; callers still need authoritative
// SQLite validation and exact reranking before exposing candidates.
type USearchRoot struct {
	Root     semanticsegment.Root
	Segments []USearchRootSegment
}

type USearchRootSegment struct {
	Manifest semanticsegment.Manifest
	Index    *USearch
}

func OpenUSearchRoot(cacheDir, databaseID, profileID, generationID string, options USearchOptions) (*USearchRoot, error) {
	root, err := semanticsegment.OpenRoot(cacheDir, databaseID, profileID, generationID)
	if err != nil {
		return nil, fmt.Errorf("open usearch root: %w", err)
	}
	loaded := &USearchRoot{Root: root, Segments: make([]USearchRootSegment, 0, len(root.Manifest.Segments))}
	fail := func(err error) (*USearchRoot, error) { _ = loaded.Close(); return nil, err }
	for _, reference := range root.Manifest.Segments {
		segment, err := semanticsegment.OpenSegment(cacheDir, databaseID, profileID, reference.Hash)
		if err != nil {
			return fail(fmt.Errorf("open usearch root segment %s: %w", reference.Hash, err))
		}
		if segment.Manifest.Backend != BackendUSearch || segment.Manifest.Dimensions != options.Dimensions {
			return fail(fmt.Errorf("usearch root segment %s backend/dimensions mismatch", reference.Hash))
		}
		payload, err := os.ReadFile(filepath.Join(cacheDir, filepath.FromSlash(reference.RelativePath), semanticsegment.PayloadFileName))
		if err != nil {
			return fail(fmt.Errorf("read usearch root payload %s: %w", reference.Hash, err))
		}
		index, err := NewUSearch(options)
		if err != nil {
			return fail(err)
		}
		if err := index.Import(bytes.NewReader(payload)); err != nil {
			_ = index.Close()
			return fail(fmt.Errorf("import usearch root segment %s: %w", reference.Hash, err))
		}
		loaded.Segments = append(loaded.Segments, USearchRootSegment{Manifest: segment.Manifest, Index: index})
	}
	return loaded, nil
}

func (r *USearchRoot) Close() error {
	if r == nil {
		return nil
	}
	var first error
	for i := range r.Segments {
		if r.Segments[i].Index != nil {
			if err := r.Segments[i].Index.Close(); err != nil && first == nil {
				first = err
			}
			r.Segments[i].Index = nil
		}
	}
	return first
}
