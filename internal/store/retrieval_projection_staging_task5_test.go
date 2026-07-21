package store

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"testing"

	"github.com/darron/dbrain/internal/retrievalchunk"
)

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
