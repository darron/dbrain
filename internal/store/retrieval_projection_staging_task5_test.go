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

func TestProjectionStagingPromoteAndBlockRejectChangedPurgeEpoch(t *testing.T) {
	t.Run("stage", func(t *testing.T) {
		st := openStoreAtPath(t, filepath.Join(t.TempDir(), "brain.db"))
		defer func() { _ = st.Close() }()
		ctx := context.Background()
		seedRetrievalSource(t, st, "source:epoch-stage")
		work, projection := projectionWorkForEpochTest(t, st, "source:epoch-stage")
		row := task5StageRowForOccurrence(t, projection, projection.Occurrences[0])

		_, err := st.StageRetrievalProjectionBatch(ctx, StageRetrievalProjectionInput{
			ParentKind: work.Parent.Kind, ParentSourceKey: work.Parent.SourceKey,
			DirtyRevision: work.DirtyRevision, ProjectionHash: projection.ParentHash,
			ExpectedPurgeEpoch: 1,
			Cursor:             retrievalchunk.Cursor{SectionKey: row.Occurrence.SectionKey, NextBoundary: row.Occurrence.EndChar},
			Rows:               []RetrievalProjectionStageRow{row},
		})
		if !errors.Is(err, ErrRetrievalPurgeEpochChanged) {
			t.Fatalf("stage err=%v want ErrRetrievalPurgeEpochChanged", err)
		}
		var staged int
		if err := st.db.QueryRow(`SELECT COUNT(*) FROM retrieval_projection_staging WHERE parent_source_key=?`, work.Parent.SourceKey).Scan(&staged); err != nil {
			t.Fatal(err)
		}
		if staged != 0 {
			t.Fatalf("stage committed %d rows after purge epoch changed", staged)
		}
	})

	t.Run("promote", func(t *testing.T) {
		st := openStoreAtPath(t, filepath.Join(t.TempDir(), "brain.db"))
		defer func() { _ = st.Close() }()
		ctx := context.Background()
		seedRetrievalSource(t, st, "source:epoch-promote")
		work, projection := projectionWorkForEpochTest(t, st, "source:epoch-promote")
		rows := make([]RetrievalProjectionStageRow, 0, len(projection.Occurrences))
		for _, occurrence := range projection.Occurrences {
			rows = append(rows, task5StageRowForOccurrence(t, projection, occurrence))
		}
		cp, err := st.StageRetrievalProjectionBatch(ctx, StageRetrievalProjectionInput{
			ParentKind: work.Parent.Kind, ParentSourceKey: work.Parent.SourceKey,
			DirtyRevision: work.DirtyRevision, ProjectionHash: projection.ParentHash,
			ExpectedPurgeEpoch: 0,
			Rows:               rows,
		})
		if err != nil {
			t.Fatal(err)
		}
		if _, err := st.db.Exec(`UPDATE retrieval_state SET purge_epoch=1 WHERE singleton=1`); err != nil {
			t.Fatal(err)
		}

		_, err = st.PromoteRetrievalProjectionStaging(ctx, cp)
		if !errors.Is(err, ErrRetrievalPurgeEpochChanged) {
			t.Fatalf("promote err=%v want ErrRetrievalPurgeEpochChanged", err)
		}
		var live int
		if err := st.db.QueryRow(`SELECT COUNT(*) FROM retrieval_chunks WHERE parent_source_key=?`, work.Parent.SourceKey).Scan(&live); err != nil {
			t.Fatal(err)
		}
		if live != 0 {
			t.Fatalf("promotion committed %d live chunks after purge epoch changed", live)
		}
	})

	t.Run("block", func(t *testing.T) {
		st := openStoreAtPath(t, filepath.Join(t.TempDir(), "brain.db"))
		defer func() { _ = st.Close() }()
		ctx := context.Background()
		seedRetrievalSource(t, st, "source:epoch-block")
		work, projection := projectionWorkForEpochTest(t, st, "source:epoch-block")
		if _, err := st.db.Exec(`UPDATE retrieval_state SET purge_epoch=1 WHERE singleton=1`); err != nil {
			t.Fatal(err)
		}

		err := st.BlockRetrievalProjectionTooLarge(
			ctx,
			work.Parent,
			work.DirtyRevision,
			projection.ParentHash,
			0,
		)
		if !errors.Is(err, ErrRetrievalPurgeEpochChanged) {
			t.Fatalf("block err=%v want ErrRetrievalPurgeEpochChanged", err)
		}
		var status string
		if err := st.db.QueryRow(`
			SELECT status FROM retrieval_parent_projections
			WHERE parent_kind=? AND parent_source_key=?`,
			work.Parent.Kind, work.Parent.SourceKey).Scan(&status); err != nil {
			t.Fatal(err)
		}
		if status == string(RetrievalProjectionBlocked) {
			t.Fatal("projection was blocked after purge epoch changed")
		}
	})
}

func TestProjectionStagingPersistsOriginalPurgeEpochAcrossReopen(t *testing.T) {
	path := filepath.Join(t.TempDir(), "brain.db")
	st := openStoreAtPath(t, path)
	ctx := context.Background()
	seedRetrievalSource(t, st, "source:epoch-resume")
	work, projection := projectionWorkForEpochTest(t, st, "source:epoch-resume")
	row := task5StageRowForOccurrence(t, projection, projection.Occurrences[0])

	cp, err := st.StageRetrievalProjectionBatch(ctx, StageRetrievalProjectionInput{
		ParentKind: work.Parent.Kind, ParentSourceKey: work.Parent.SourceKey,
		DirtyRevision: work.DirtyRevision, ProjectionHash: projection.ParentHash,
		ExpectedPurgeEpoch: 0,
		Cursor:             retrievalchunk.Cursor{SectionKey: row.Occurrence.SectionKey, NextBoundary: row.Occurrence.EndChar},
		Rows:               []RetrievalProjectionStageRow{row},
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := st.Close(); err != nil {
		t.Fatal(err)
	}

	st = openStoreAtPath(t, path)
	defer func() { _ = st.Close() }()
	loaded, ok, err := st.LoadRetrievalProjectionStaging(ctx, work.Parent, work.DirtyRevision)
	if err != nil || !ok {
		t.Fatalf("load persisted checkpoint ok=%v err=%v", ok, err)
	}
	if loaded.ExpectedPurgeEpoch != cp.ExpectedPurgeEpoch {
		t.Fatalf("loaded purge epoch=%d want original %d", loaded.ExpectedPurgeEpoch, cp.ExpectedPurgeEpoch)
	}
	var minimum, maximum int64
	if err := st.db.QueryRow(`
		SELECT MIN(expected_purge_epoch), MAX(expected_purge_epoch)
		FROM retrieval_projection_staging
		WHERE work_id=?`, cp.WorkID).Scan(&minimum, &maximum); err != nil {
		t.Fatal(err)
	}
	if minimum != cp.ExpectedPurgeEpoch || maximum != cp.ExpectedPurgeEpoch {
		t.Fatalf("durable staging epochs min=%d max=%d want=%d", minimum, maximum, cp.ExpectedPurgeEpoch)
	}

	if _, err := st.db.Exec(`UPDATE retrieval_state SET purge_epoch=1 WHERE singleton=1`); err != nil {
		t.Fatal(err)
	}
	_, _, err = st.LoadRetrievalProjectionStaging(ctx, work.Parent, work.DirtyRevision)
	if !errors.Is(err, ErrRetrievalPurgeEpochChanged) {
		t.Fatalf("load after purge err=%v want ErrRetrievalPurgeEpochChanged", err)
	}
}

func projectionWorkForEpochTest(
	t *testing.T,
	st *Store,
	sourceKey string,
) (RetrievalParentWork, retrievalchunk.Projection) {
	t.Helper()
	work, err := st.ListDirtyRetrievalParents(
		context.Background(),
		projectionRevisionForTest(t, st),
		1,
	)
	if err != nil || len(work) != 1 {
		t.Fatalf("work=%+v err=%v", work, err)
	}
	if work[0].Parent.SourceKey != sourceKey {
		t.Fatalf("source key=%q want=%q", work[0].Parent.SourceKey, sourceKey)
	}
	projection, err := retrievalchunk.BuildProjection(work[0].Parent, retrievalchunk.DefaultOptions())
	if err != nil || len(projection.Occurrences) == 0 {
		t.Fatalf("projection=%+v err=%v", projection, err)
	}
	return work[0], projection
}

func TestTask5StoreStagingFullyValidatesOncePerLifecycleBoundary(t *testing.T) {
	st := openStoreAtPath(t, filepath.Join(t.TempDir(), "brain.db"))
	defer func() { _ = st.Close() }()
	ctx := context.Background()
	seedRetrievalSource(t, st, "source:task5-validation-lifecycle")
	if _, err := st.db.Exec(`UPDATE sources SET extracted_text=? WHERE source_key='source:task5-validation-lifecycle'`, strings.Repeat("unique semantic evidence sentence. ", 1_000)); err != nil {
		t.Fatal(err)
	}
	work, err := st.ListDirtyRetrievalParents(ctx, projectionRevisionForTest(t, st), 1)
	if err != nil || len(work) != 1 {
		t.Fatalf("work=%+v err=%v", work, err)
	}
	projection, err := retrievalchunk.BuildProjection(work[0].Parent, retrievalchunk.DefaultOptions())
	if err != nil || len(projection.Occurrences) < 4 {
		t.Fatalf("occurrences=%d err=%v", len(projection.Occurrences), err)
	}
	session, err := retrievalchunk.PrepareStreamCommandSessionContext(ctx, work[0].Parent, retrievalchunk.DefaultOptions(), MaxRetrievalProjectionOccurrences)
	if err != nil {
		t.Fatal(err)
	}
	encodedPlan, planDigest, err := session.MarshalPlanBinary()
	if err != nil {
		t.Fatal(err)
	}
	rows := make([]RetrievalProjectionStageRow, 0, len(projection.Occurrences))
	for _, occurrence := range projection.Occurrences {
		rows = append(rows, task5StageRowForOccurrence(t, projection, occurrence))
	}
	fullValidations := 0
	planHashCalls := 0
	planHashBytes := 0
	st.retrievalProjectionFullValidation = func() { fullValidations++ }
	st.retrievalProjectionPlanHashObserved = func(size int) {
		planHashCalls++
		planHashBytes += size
	}

	var cp RetrievalProjectionCheckpoint
	for start := 0; start < len(rows); start += 2 {
		end := min(start+2, len(rows))
		cursor := retrievalchunk.Cursor{}
		if end < len(rows) {
			cursor = retrievalchunk.Cursor{SectionKey: rows[end-1].Occurrence.SectionKey, NextBoundary: rows[end-1].Occurrence.EndChar}
		}
		input := StageRetrievalProjectionInput{
			ParentKind: work[0].Parent.Kind, ParentSourceKey: work[0].Parent.SourceKey,
			DirtyRevision: work[0].DirtyRevision, ProjectionHash: projection.ParentHash,
			Cursor: cursor, Rows: rows[start:end], PreparedPlanDigest: planDigest,
		}
		if start == 0 {
			input.PreparedPlan = encodedPlan
		}
		if cp.WorkID != "" {
			input.WorkID = cp.WorkID
		}
		cp, err = st.StageRetrievalProjectionBatch(ctx, input)
		if err != nil {
			t.Fatalf("stage batch %d: %v", start/2, err)
		}
		if fullValidations != 1 {
			t.Fatalf("stage batch %d full validations=%d want 1", start/2, fullValidations)
		}
		if planHashCalls != 1 || planHashBytes != len(encodedPlan) {
			t.Fatalf("stage batch %d plan hash calls=%d bytes=%d want 1/%d", start/2, planHashCalls, planHashBytes, len(encodedPlan))
		}
	}
	loaded, ok, err := st.LoadRetrievalProjectionStaging(ctx, work[0].Parent, work[0].DirtyRevision)
	if err != nil || !ok || fullValidations != 2 {
		t.Fatalf("load ok=%v err=%v full_validations=%d", ok, err, fullValidations)
	}
	if planHashCalls != 2 || planHashBytes != 2*len(encodedPlan) {
		t.Fatalf("load plan hash calls=%d bytes=%d want 2/%d", planHashCalls, planHashBytes, 2*len(encodedPlan))
	}
	if _, err := st.PromoteRetrievalProjectionStaging(ctx, loaded); err != nil || fullValidations != 3 {
		t.Fatalf("promote err=%v full_validations=%d", err, fullValidations)
	}
	if planHashCalls != 3 || planHashBytes != 3*len(encodedPlan) {
		t.Fatalf("promotion plan hash calls=%d bytes=%d want 3/%d", planHashCalls, planHashBytes, 3*len(encodedPlan))
	}

	tx, err := st.db.BeginTx(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = tx.Rollback() }()
	canceled, cancel := context.WithCancel(context.Background())
	cancel()
	_, err = st.validateRetrievalProjectionStagingWorkTx(canceled, tx, work[0].Parent.Kind, work[0].Parent.SourceKey, work[0].DirtyRevision, projection.ParentHash)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled full validation err=%v", err)
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
	_, err := buildBoundedAuthoritativeProjection(context.Background(), parent, retrievalchunk.Options{TargetRunes: 10, MaxRunes: 12}, 4)
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
