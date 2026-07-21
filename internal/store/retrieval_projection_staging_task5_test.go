package store

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/darron/dbrain/internal/retrievalchunk"
)

func TestTask5StagedBytesCountUTF8Bytes(t *testing.T) {
	st := openStoreAtPath(t, filepath.Join(t.TempDir(), "brain.db"))
	defer func() { _ = st.Close() }()
	ctx := context.Background()
	seedRetrievalSource(t, st, "source:task5-utf8-bytes")
	if _, err := st.db.Exec(`
		UPDATE sources SET extracted_text=?
		WHERE source_key='source:task5-utf8-bytes'`, strings.Repeat("semantic 🧠 evidence 漢字. ", 20)); err != nil {
		t.Fatal(err)
	}
	work, err := st.ListDirtyRetrievalParents(ctx, projectionRevisionForTest(t, st), 1)
	if err != nil || len(work) != 1 {
		t.Fatalf("work=%+v err=%v", work, err)
	}
	projection, err := retrievalchunk.BuildProjection(work[0].Parent, retrievalchunk.DefaultOptions())
	if err != nil || len(projection.Occurrences) == 0 {
		t.Fatalf("occurrences=%d err=%v", len(projection.Occurrences), err)
	}
	row := task5StageRowForOccurrence(t, projection, projection.Occurrences[0])
	cp, err := st.StageRetrievalProjectionBatch(ctx, StageRetrievalProjectionInput{
		ParentKind: work[0].Parent.Kind, ParentSourceKey: work[0].Parent.SourceKey,
		DirtyRevision: work[0].DirtyRevision, ProjectionHash: projection.ParentHash,
		Cursor: retrievalchunk.Cursor{SectionKey: row.Occurrence.SectionKey, NextBoundary: row.Occurrence.EndChar},
		Rows:   []RetrievalProjectionStageRow{row},
	})
	if err != nil {
		t.Fatal(err)
	}
	cp, err = st.StageRetrievalProjectionBatch(ctx, StageRetrievalProjectionInput{
		WorkID: cp.WorkID, ParentKind: cp.ParentKind, ParentSourceKey: cp.ParentSourceKey,
		DirtyRevision: cp.DirtyRevision, ProjectionHash: cp.ProjectionHash,
	})
	if err != nil {
		t.Fatal(err)
	}
	chunkJSON, err := json.Marshal(row.Chunk)
	if err != nil {
		t.Fatal(err)
	}
	occurrenceJSON, err := json.Marshal(row.Occurrence)
	if err != nil {
		t.Fatal(err)
	}
	want := int64(len(chunkJSON) + len(occurrenceJSON))
	if cp.StagedBytes != want {
		t.Fatalf("staged bytes=%d want UTF-8 byte length %d", cp.StagedBytes, want)
	}

	loaded, ok, err := st.LoadRetrievalProjectionStaging(ctx, work[0].Parent, work[0].DirtyRevision)
	if err != nil || !ok {
		t.Fatalf("load checkpoint ok=%v err=%v", ok, err)
	}
	if loaded.StagedBytes != want {
		t.Fatalf("loaded staged bytes=%d want UTF-8 byte length %d", loaded.StagedBytes, want)
	}
	runeCount := int64(utf8.RuneCount(chunkJSON) + utf8.RuneCount(occurrenceJSON))
	if runeCount >= want {
		t.Fatalf("fixture rune count=%d must be below byte count=%d", runeCount, want)
	}
	byteLimit := int(runeCount + (want-runeCount)/2)
	_, err = st.promoteRetrievalProjectionStagingWithByteLimit(ctx, loaded, byteLimit)
	var tooLarge *RetrievalProjectionTooLargeError
	if !errors.As(err, &tooLarge) || tooLarge.ByteCount != want || tooLarge.Limit != byteLimit {
		t.Fatalf("multibyte promotion err=%v too_large=%+v want bytes=%d limit=%d", err, tooLarge, want, byteLimit)
	}
}

func TestTask5PromotionRejectsIncompleteAndFabricatedStaging(t *testing.T) {
	for _, tc := range []struct {
		name string
		rows func(retrievalchunk.Projection) []RetrievalProjectionStageRow
	}{
		{
			name: "incomplete prefix",
			rows: func(projection retrievalchunk.Projection) []RetrievalProjectionStageRow {
				return []RetrievalProjectionStageRow{task5StageRowForOccurrence(t, projection, projection.Occurrences[0])}
			},
		},
		{
			name: "fabricated chunk text",
			rows: func(projection retrievalchunk.Projection) []RetrievalProjectionStageRow {
				rows := make([]RetrievalProjectionStageRow, 0, len(projection.Occurrences))
				for _, occurrence := range projection.Occurrences {
					rows = append(rows, task5StageRowForOccurrence(t, projection, occurrence))
				}
				rows[0].Chunk.Text = "fabricated derived text"
				rows[0].Chunk.TextHash = strings.Repeat("f", 64)
				return rows
			},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			st := openStoreAtPath(t, filepath.Join(t.TempDir(), "brain.db"))
			defer func() { _ = st.Close() }()
			ctx := context.Background()
			seedRetrievalSource(t, st, "source:task5-adversarial")
			var text strings.Builder
			for i := 0; i < 900; i++ {
				_, _ = fmt.Fprintf(&text, "unique-window-%04d evidence sentence. ", i)
			}
			if _, err := st.db.Exec(`UPDATE sources SET extracted_text=? WHERE source_key='source:task5-adversarial'`, text.String()); err != nil {
				t.Fatal(err)
			}
			work, err := st.ListDirtyRetrievalParents(ctx, projectionRevisionForTest(t, st), 1)
			if err != nil || len(work) != 1 {
				t.Fatalf("work=%+v err=%v", work, err)
			}
			projection, err := retrievalchunk.BuildProjection(work[0].Parent, retrievalchunk.DefaultOptions())
			if err != nil || len(projection.Chunks) < 2 {
				t.Fatalf("chunks=%d err=%v", len(projection.Chunks), err)
			}
			cp, err := st.StageRetrievalProjectionBatch(ctx, StageRetrievalProjectionInput{
				ParentKind: "source", ParentSourceKey: "source:task5-adversarial",
				DirtyRevision: work[0].DirtyRevision, ProjectionHash: projection.ParentHash,
				Cursor: retrievalchunk.Cursor{SectionKey: projection.Occurrences[0].SectionKey, NextBoundary: projection.Occurrences[0].EndChar},
				Rows:   tc.rows(projection),
			})
			if err != nil {
				t.Fatal(err)
			}
			cp, err = st.StageRetrievalProjectionBatch(ctx, StageRetrievalProjectionInput{
				WorkID: cp.WorkID, ParentKind: cp.ParentKind, ParentSourceKey: cp.ParentSourceKey,
				DirtyRevision: cp.DirtyRevision, ProjectionHash: cp.ProjectionHash,
			})
			if err != nil {
				t.Fatal(err)
			}
			if _, err := st.PromoteRetrievalProjectionStaging(ctx, cp); err == nil || !strings.Contains(err.Error(), "authoritative projection") {
				t.Fatalf("promotion err=%v", err)
			}
			var live int
			if err := st.db.QueryRow(`SELECT COUNT(*) FROM retrieval_chunks WHERE parent_source_key='source:task5-adversarial'`).Scan(&live); err != nil || live != 0 {
				t.Fatalf("live=%d err=%v", live, err)
			}
		})
	}
}

func TestTask5BoundedAuthoritativeProjectionStopsAboveLimit(t *testing.T) {
	var text strings.Builder
	for i := 0; i < 100; i++ {
		_, _ = fmt.Fprintf(&text, "%010d ", i)
	}
	parent := retrievalchunk.Parent{Kind: "source", SourceKey: "source:task5-limit", ContentHash: "v1", Sections: []retrievalchunk.Section{{Key: "body", Role: "raw", Text: text.String()}}}
	_, err := buildBoundedAuthoritativeProjection(parent, retrievalchunk.Options{TargetRunes: 10, MaxRunes: 12}, 4)
	var tooLarge *RetrievalProjectionTooLargeError
	if !errors.As(err, &tooLarge) || tooLarge.ChunkCount != 5 {
		t.Fatalf("err=%v too_large=%+v", err, tooLarge)
	}
}

func task5StageRowForOccurrence(t *testing.T, projection retrievalchunk.Projection, occurrence retrievalchunk.Occurrence) RetrievalProjectionStageRow {
	t.Helper()
	for _, chunk := range projection.Chunks {
		if chunk.ID == occurrence.ChunkID {
			return RetrievalProjectionStageRow{Chunk: chunk, Occurrence: occurrence}
		}
	}
	t.Fatalf("missing chunk for occurrence %+v", occurrence)
	return RetrievalProjectionStageRow{}
}
