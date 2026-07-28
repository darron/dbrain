package researchsemantic

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/darron/dbrain/internal/embedding"
	"github.com/darron/dbrain/internal/retrieval"
	"github.com/darron/dbrain/internal/semanticindex"
	"github.com/darron/dbrain/internal/store"
)

type Hydrator interface {
	HydrateRetrievalChunks(context.Context, []string) ([]store.RetrievalChunkEvidenceRow, error)
}

type Options struct {
	Profile   embedding.Profile
	Limit     int
	MaxChunks int
	Filters   semanticindex.Filters
	Timeout   time.Duration
}

const DefaultQueryTimeout = 15 * time.Second

const ReasonGenerationBusy semanticindex.StatusReason = "generation_busy"

type GenerationLease interface {
	Close() error
}

type GenerationLeaseAcquirer func(context.Context) (GenerationLease, error)

type Retriever struct {
	provider               embedding.Provider
	searcher               semanticindex.Searcher
	hydrator               Hydrator
	acquireGenerationLease GenerationLeaseAcquirer
	closeOnce              sync.Once
	closeErr               error
}

func New(provider embedding.Provider, searcher semanticindex.Searcher, hydrator Hydrator) *Retriever {
	return NewWithGenerationLease(provider, searcher, hydrator, nil)
}

func NewWithGenerationLease(provider embedding.Provider, searcher semanticindex.Searcher, hydrator Hydrator, acquire GenerationLeaseAcquirer) *Retriever {
	return &Retriever{
		provider: provider, searcher: searcher, hydrator: hydrator,
		acquireGenerationLease: acquire,
	}
}

// Close releases optional native search resources. Exact searchers have no
// close operation, so the method is safe for every retriever configuration.
func (r *Retriever) Close() error {
	if r == nil {
		return nil
	}
	r.closeOnce.Do(func() {
		if closer, ok := r.searcher.(interface{ Close() error }); ok {
			r.closeErr = closer.Close()
		}
	})
	return r.closeErr
}

func (r *Retriever) Retrieve(ctx context.Context, query string, opts Options) ([]retrieval.EvidenceDocument, semanticindex.Status, error) {
	empty := make([]retrieval.EvidenceDocument, 0)
	unavailable := semanticindex.Status{State: semanticindex.StateUnavailable, Backend: semanticindex.BackendExact}
	if err := ctx.Err(); err != nil {
		unavailable.Reason = semanticindex.ReasonCanceled
		return empty, unavailable, err
	}
	query = strings.TrimSpace(query)
	if query == "" || r == nil || r.provider == nil || r.searcher == nil || r.hydrator == nil {
		unavailable.Reason = semanticindex.ReasonQueryEmbeddingFailed
		return empty, unavailable, nil
	}
	profileID, err := opts.Profile.ID()
	if err != nil || opts.Profile.Normalization != embedding.NormalizationL2 {
		unavailable.Reason = semanticindex.ReasonProfileMismatch
		return empty, unavailable, nil
	}
	unavailable.ProfileID = profileID
	info := r.provider.Info()
	if info.Provider != strings.TrimSpace(opts.Profile.Provider) || info.Model != strings.TrimSpace(opts.Profile.Model) || info.Dimensions != opts.Profile.Dimensions {
		unavailable.Reason = semanticindex.ReasonProfileMismatch
		return empty, unavailable, nil
	}
	req := embedding.Request{Texts: []string{query}, Purpose: embedding.PurposeQuery}
	response, embedErr := r.provider.Embed(ctx, req)
	if embedErr != nil {
		if err := ctx.Err(); err != nil {
			unavailable.Reason = semanticindex.ReasonCanceled
			return empty, unavailable, err
		}
		if embedding.IsRetryable(embedErr) {
			unavailable.Reason = semanticindex.ReasonProviderUnavailable
			return empty, unavailable, nil
		}
		if errors.Is(embedErr, context.Canceled) || errors.Is(embedErr, context.DeadlineExceeded) {
			unavailable.Reason = semanticindex.ReasonCanceled
			return empty, unavailable, embedErr
		}
		unavailable.Reason = semanticindex.ReasonQueryEmbeddingFailed
		return empty, unavailable, nil
	}
	if err := embedding.ValidateResponse(info, req, response); err != nil || len(response.Vectors) != 1 {
		unavailable.Reason = semanticindex.ReasonQueryEmbeddingFailed
		return empty, unavailable, nil
	}
	queryVector := response.Vectors[0]
	if err := embedding.ValidateDenseF32(queryVector, opts.Profile.Dimensions, embedding.NormalizationL2); err != nil {
		unavailable.Reason = semanticindex.ReasonQueryEmbeddingFailed
		return empty, unavailable, nil
	}
	return r.retrieveAdmitted(ctx, queryVector, opts, info, profileID, unavailable)
}

func (r *Retriever) retrieveAdmitted(
	ctx context.Context,
	queryVector []float32,
	opts Options,
	info embedding.Info,
	profileID string,
	unavailable semanticindex.Status,
) ([]retrieval.EvidenceDocument, semanticindex.Status, error) {
	empty := make([]retrieval.EvidenceDocument, 0)
	var lease GenerationLease
	if r.acquireGenerationLease != nil {
		acquired, err := r.acquireGenerationLease(ctx)
		if err != nil {
			if ctxErr := ctx.Err(); ctxErr != nil {
				unavailable.Reason = semanticindex.ReasonCanceled
				return empty, unavailable, ctxErr
			}
			if errors.Is(err, context.Canceled) {
				unavailable.Reason = semanticindex.ReasonCanceled
				return empty, unavailable, err
			}
			if errors.Is(err, context.DeadlineExceeded) {
				unavailable.Reason = ReasonGenerationBusy
				return empty, unavailable, nil
			}
			unavailable.Reason = semanticindex.ReasonSearchError
			return empty, unavailable, fmt.Errorf("acquire semantic generation lease: %w", err)
		}
		if acquired == nil {
			unavailable.Reason = semanticindex.ReasonSearchError
			return empty, unavailable, errors.New("acquire semantic generation lease returned nil lease")
		}
		lease = acquired
	}

	docs, status, retrieveErr := r.searchAndHydrate(ctx, queryVector, opts, info, profileID)
	if lease != nil {
		if releaseErr := lease.Close(); releaseErr != nil {
			unavailable.Reason = semanticindex.ReasonSearchError
			return empty, unavailable, errors.Join(retrieveErr, fmt.Errorf("release semantic generation lease: %w", releaseErr))
		}
	}
	return docs, status, retrieveErr
}

func (r *Retriever) searchAndHydrate(
	ctx context.Context,
	queryVector []float32,
	opts Options,
	info embedding.Info,
	profileID string,
) ([]retrieval.EvidenceDocument, semanticindex.Status, error) {
	empty := make([]retrieval.EvidenceDocument, 0)
	hits, status, searchErr := r.searcher.Search(ctx, queryVector, semanticindex.SearchOptions{
		Profile: opts.Profile, Limit: opts.Limit, MaxChunks: opts.MaxChunks, Filters: opts.Filters,
	})
	if searchErr != nil {
		if err := ctx.Err(); err != nil {
			status = semanticindex.Status{State: semanticindex.StateUnavailable, Reason: semanticindex.ReasonCanceled, Backend: semanticindex.BackendExact, ProfileID: profileID}
			return empty, status, err
		}
		if status.State == "" {
			status = semanticindex.Status{State: semanticindex.StateUnavailable, Reason: semanticindex.ReasonSearchError, Backend: semanticindex.BackendExact, ProfileID: profileID}
		}
		return empty, status, searchErr
	}
	if status.State != semanticindex.StateSearched || len(hits) == 0 {
		return empty, status, nil
	}
	chunkIDs := make([]string, len(hits))
	for i := range hits {
		chunkIDs[i] = hits[i].ChunkID
	}
	rows, err := r.hydrator.HydrateRetrievalChunks(ctx, chunkIDs)
	if err != nil {
		if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
			status.State = semanticindex.StateUnavailable
			status.Reason = semanticindex.ReasonCanceled
			return empty, status, err
		}
		status.State = semanticindex.StateUnavailable
		status.Reason = semanticindex.ReasonSearchError
		return empty, status, fmt.Errorf("hydrate semantic retrieval hits: %w", err)
	}
	byID := make(map[string]store.RetrievalChunkEvidenceRow, len(rows))
	for _, row := range rows {
		byID[row.ChunkID] = row
	}
	docs := make([]retrieval.EvidenceDocument, 0, len(rows))
	for _, hit := range hits {
		if err := ctx.Err(); err != nil {
			status.State = semanticindex.StateUnavailable
			status.Reason = semanticindex.ReasonCanceled
			return empty, status, err
		}
		row, ok := byID[hit.ChunkID]
		if !ok {
			continue
		}
		distance := hit.Distance
		docs = append(docs, retrieval.EvidenceDocument{
			SourceKey: row.ParentSourceKey, Kind: row.ParentKind, Title: row.Title, URL: row.URL,
			NotePath: row.NotePath, Summary: row.Summary, Excerpt: row.Text, Author: row.Author,
			SourceType: hit.SourceType, PublishedAt: row.PublishedAt, ExtractedAt: row.ExtractedAt,
			SummarizedAt: row.SummarizedAt, UserTags: row.UserTags, EvidenceRole: row.EvidenceRole,
			Chunk: &retrieval.EvidenceChunk{
				ID: hit.ChunkID, ParentSourceKey: row.ParentSourceKey, Index: row.Ordinal, SectionOrdinal: hit.SectionOrdinal, StartChar: row.StartChar,
				EndChar: row.EndChar, Role: row.EvidenceRole, Hash: row.ChunkTextHash, Heading: row.Heading,
				ContributingIDs: []string{hit.ChunkID}, WindowHash: retrieval.WindowHash([]string{hit.ChunkID}, []string{row.ChunkTextHash}, row.Text),
			},
			Retrieval: &retrieval.RetrievalInfo{Lanes: []retrieval.RetrievalLane{{
				Name: "semantic", Status: "used", Provider: info.Provider, Rank: hit.Rank,
				RawDistance: &distance, Profile: status.ProfileID, Backend: status.Backend,
				Generation: status.GenerationID,
			}}},
		})
	}
	return docs, status, nil
}
