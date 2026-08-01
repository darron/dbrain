package semanticbuild

import (
	"context"
	"fmt"
	"sort"

	"github.com/darron/dbrain/internal/embedding"
	"github.com/darron/dbrain/internal/semanticsegment"
	"github.com/darron/dbrain/internal/store"
)

// CompactionStore is the non-serving persistence boundary for one bounded
// physical compaction. The executor will use only the selected snapshot rows
// and existing generation activation CAS.
type CompactionStore interface {
	RetrievalDatabaseID(context.Context) (string, error)
	RetrievalActiveSegmentCompactionSnapshot(context.Context, string) (store.RetrievalActiveSegmentCompactionSnapshot, error)
	StreamRetrievalActiveSegmentMembers(context.Context, store.RetrievalActiveSegmentMemberStreamRequest, store.RetrievalActiveSegmentMemberVisitor) (int, error)
	RetrievalIndexGenerationSegments(context.Context, string) ([]store.RetrievalIndexSegmentRow, error)
	CompleteRetrievalIndexGeneration(context.Context, store.CompleteRetrievalIndexGenerationInput) error
}

// CompactionOptions identifies the immutable embedding profile and derived
// backend provenance for one replacement-root attempt.
type CompactionOptions struct {
	Profile                                           embedding.Profile
	Backend, BackendVersion, DistanceMetric, CacheDir string
}

// CompactionResult records the pure selection outcome. Later fields are added
// only after payload/root publication succeeds.
type CompactionResult struct {
	Plan                         SegmentCompactionPlan
	GenerationID                 string
	ReplacementSegmentHashes     []string
	Indexed, StreamedLiveMembers int
}

// Compact is the entrypoint for bounded physical compaction. The initial
// implementation establishes the injected, non-serving selection boundary; it
// never falls back to a whole-corpus scan.
func Compact(ctx context.Context, st CompactionStore, builder StreamingSegmentPayloadBuilder, opts CompactionOptions) (CompactionResult, error) {
	if err := ctx.Err(); err != nil {
		return CompactionResult{Plan: SegmentCompactionPlan{Kind: SegmentCompactionNone, Inputs: []SegmentCompactionInput{}, Outputs: []SegmentCompactionOutput{}}}, err
	}
	if st == nil {
		return CompactionResult{Plan: SegmentCompactionPlan{Kind: SegmentCompactionNone, Inputs: []SegmentCompactionInput{}, Outputs: []SegmentCompactionOutput{}}}, fmt.Errorf("semantic compaction store is required")
	}
	profileID, err := opts.Profile.ID()
	if err != nil {
		return CompactionResult{}, fmt.Errorf("semantic compaction profile is invalid: %w", err)
	}
	snapshot, err := st.RetrievalActiveSegmentCompactionSnapshot(ctx, profileID)
	if err != nil {
		return CompactionResult{}, err
	}
	inputs := make([]SegmentCompactionInput, 0, len(snapshot.Segments))
	for _, segment := range snapshot.Segments {
		inputs = append(inputs, SegmentCompactionInput{SegmentHash: segment.SegmentHash, CreatedOrder: segment.CreatedOrder, LiveCount: segment.LiveCount, TombstoneCount: segment.TombstoneCount})
	}
	plan, err := PlanSegmentCompaction(inputs)
	if err != nil {
		return CompactionResult{}, err
	}
	if plan.Kind == SegmentCompactionNone {
		return CompactionResult{Plan: plan}, nil
	}
	physicalOutputs := make([]SegmentCompactionOutput, 0, len(plan.Outputs))
	for _, output := range plan.Outputs {
		if output.Class != SegmentCompactionClassExactL0 {
			physicalOutputs = append(physicalOutputs, output)
		}
	}
	if len(physicalOutputs) > 0 && builder == nil {
		return CompactionResult{Plan: plan}, fmt.Errorf("semantic compaction streaming payload builder is required")
	}
	if opts.Backend == "" || opts.BackendVersion == "" || opts.DistanceMetric == "" || opts.CacheDir == "" {
		return CompactionResult{Plan: plan}, fmt.Errorf("semantic compaction backend metadata and cache directory are required")
	}
	databaseID, err := st.RetrievalDatabaseID(ctx)
	if err != nil {
		return CompactionResult{Plan: plan}, err
	}
	selected := make(map[string]struct{}, len(plan.Inputs))
	hashes := make([]string, 0, len(plan.Inputs))
	expectedLive := 0
	for _, input := range plan.Inputs {
		selected[input.SegmentHash] = struct{}{}
		hashes = append(hashes, input.SegmentHash)
		expectedLive += input.LiveCount
	}
	type outputBuild struct {
		output  SegmentCompactionOutput
		session StreamingSegmentPayloadSession
		members []semanticsegment.Member
		rows    []store.RetrievalIndexSegmentMember
	}
	builds := make([]outputBuild, len(plan.Outputs))
	for index, output := range plan.Outputs {
		builds[index].output = output
		if output.Class == SegmentCompactionClassExactL0 {
			continue
		}
		session, err := builder.Begin(ctx, output.LiveCount)
		if err != nil {
			return CompactionResult{Plan: plan}, fmt.Errorf("begin semantic compaction payload %d: %w", index, err)
		}
		builds[index].session = session
	}
	defer func() {
		for _, build := range builds {
			if build.session != nil {
				_ = build.session.Close()
			}
		}
	}()
	outputIndex, outputOffset := 0, 0
	streamed, err := st.StreamRetrievalActiveSegmentMembers(ctx, store.RetrievalActiveSegmentMemberStreamRequest{ProfileID: profileID, ExpectedActiveGenerationID: snapshot.Profile.ActiveGenerationID, ExpectedPurgeEpoch: snapshot.Profile.PurgeEpoch, ExpectedActiveSnapshotRevision: snapshot.Profile.ActiveSnapshotRevision, SegmentHashes: hashes}, func(member store.RetrievalActiveSegmentMember) error {
		for outputIndex < len(builds) && outputOffset == builds[outputIndex].output.LiveCount {
			outputIndex++
			outputOffset = 0
		}
		if outputIndex >= len(builds) {
			return fmt.Errorf("semantic compaction stream exceeds planned live count")
		}
		build := &builds[outputIndex]
		if build.session != nil {
			if err := build.session.Add(ctx, member.Embedding); err != nil {
				return err
			}
			ordinal := uint64(len(build.members))
			build.members = append(build.members, semanticsegment.Member{Ordinal: ordinal, ChunkID: member.Embedding.ChunkID, Revision: member.Embedding.Revision, VectorHash: member.Embedding.VectorHash})
			build.rows = append(build.rows, store.RetrievalIndexSegmentMember{Ordinal: ordinal, ChunkID: member.Embedding.ChunkID, Revision: member.Embedding.Revision, VectorHash: member.Embedding.VectorHash})
		}
		outputOffset++
		return nil
	})
	if err != nil {
		return CompactionResult{Plan: plan}, fmt.Errorf("stream semantic compaction members: %w", err)
	}
	if streamed != expectedLive {
		return CompactionResult{Plan: plan}, fmt.Errorf("semantic compaction streamed %d live members, want %d", streamed, expectedLive)
	}
	segments, err := st.RetrievalIndexGenerationSegments(ctx, snapshot.Profile.ActiveGenerationID)
	if err != nil {
		return CompactionResult{Plan: plan}, err
	}
	retained := make([]store.RetrievalIndexSegmentRow, 0, len(segments)+len(builds))
	replacementHashes := make([]string, 0, len(builds))
	for _, segment := range segments {
		if _, chosen := selected[segment.SegmentHash]; !chosen {
			retained = append(retained, segment)
		}
	}
	newMembers := make([]store.RetrievalIndexSegmentMember, 0)
	for index := range builds {
		build := &builds[index]
		if build.session == nil {
			continue
		}
		payload, err := build.session.Finish(ctx)
		if err != nil {
			return CompactionResult{Plan: plan}, fmt.Errorf("finish semantic compaction payload %d: %w", index, err)
		}
		segment, err := semanticsegment.PublishSegment(opts.CacheDir, semanticsegment.SegmentInput{DatabaseID: databaseID, ProfileID: profileID, Backend: opts.Backend, BackendVersion: opts.BackendVersion, DistanceMetric: opts.DistanceMetric, Dimensions: opts.Profile.Dimensions, Members: build.members, Payload: payload})
		if err != nil {
			return CompactionResult{Plan: plan}, err
		}
		if _, err := semanticsegment.OpenSegment(opts.CacheDir, databaseID, profileID, segment.Hash); err != nil {
			return CompactionResult{Plan: plan}, err
		}
		row := store.RetrievalIndexSegmentRow{SegmentHash: segment.Hash, ProfileID: profileID, Backend: opts.Backend, BackendVersion: opts.BackendVersion, Dimensions: opts.Profile.Dimensions, DistanceMetric: opts.DistanceMetric, IndexedChunkCount: len(build.members), RelativeCachePath: segment.RelativePath, MembershipHash: segment.Manifest.MembersSHA256, PayloadHash: segment.Manifest.PayloadSHA256, ManifestHash: segment.Manifest.DescriptorSHA256}
		retained = append(retained, row)
		replacementHashes = append(replacementHashes, segment.Hash)
		for memberIndex := range build.rows {
			build.rows[memberIndex].SegmentHash = segment.Hash
		}
		newMembers = append(newMembers, build.rows...)
	}
	if len(retained) == 0 {
		return CompactionResult{Plan: plan}, fmt.Errorf("semantic compaction would leave an empty root")
	}
	sort.Slice(retained, func(i, j int) bool { return retained[i].SegmentHash < retained[j].SegmentHash })
	rootSegments := make([]semanticsegment.RootSegment, 0, len(retained))
	indexed := 0
	for _, segment := range retained {
		rootSegments = append(rootSegments, semanticsegment.RootSegment{Hash: segment.SegmentHash, RelativePath: segment.RelativeCachePath})
		indexed += segment.IndexedChunkCount
	}
	generationID, err := newGenerationID()
	if err != nil {
		return CompactionResult{Plan: plan}, err
	}
	root, err := semanticsegment.PublishRoot(opts.CacheDir, semanticsegment.RootInput{DatabaseID: databaseID, ProfileID: profileID, GenerationID: generationID, SnapshotRevision: snapshot.Profile.ActiveSnapshotRevision, PurgeEpoch: snapshot.Profile.PurgeEpoch, Segments: rootSegments})
	if err != nil {
		return CompactionResult{Plan: plan}, err
	}
	if _, err := semanticsegment.OpenRoot(opts.CacheDir, databaseID, profileID, generationID); err != nil {
		return CompactionResult{Plan: plan}, err
	}
	now := snapshot.Profile.ActiveSnapshotRevision
	if err := st.CompleteRetrievalIndexGeneration(ctx, store.CompleteRetrievalIndexGenerationInput{Generation: store.RetrievalIndexGenerationRow{GenerationID: generationID, ProfileID: profileID, Backend: opts.Backend, BackendVersion: opts.BackendVersion, Dimensions: opts.Profile.Dimensions, DistanceMetric: opts.DistanceMetric, IndexedChunkCount: indexed, SourceManifestHash: root.Manifest.DescriptorSHA256, BuildStatus: store.RetrievalGenerationCompleted, RelativeCachePath: root.RelativePath}, Segments: retained, Members: newMembers, SnapshotRevision: now, ExpectedActiveGenerationID: snapshot.Profile.ActiveGenerationID, ExpectedPurgeEpoch: snapshot.Profile.PurgeEpoch, ExpectedActiveSnapshotRevision: snapshot.Profile.ActiveSnapshotRevision, ActivationMode: store.RetrievalGenerationRewriteSnapshot}); err != nil {
		return CompactionResult{Plan: plan}, fmt.Errorf("activate compacted semantic root: %w", err)
	}
	result := CompactionResult{Plan: plan, GenerationID: generationID, Indexed: indexed, StreamedLiveMembers: streamed, ReplacementSegmentHashes: replacementHashes}
	return result, nil
}
