package researchsemantic

import (
	"context"
	"errors"
	"fmt"
	"strings"

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
}

type Retriever struct {
	provider embedding.Provider
	searcher semanticindex.Searcher
	hydrator Hydrator
}

func New(provider embedding.Provider, searcher semanticindex.Searcher, hydrator Hydrator) *Retriever {
	return &Retriever{provider: provider, searcher: searcher, hydrator: hydrator}
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
		if errors.Is(embedErr, context.Canceled) || errors.Is(embedErr, context.DeadlineExceeded) {
			unavailable.Reason = semanticindex.ReasonCanceled
			return empty, unavailable, embedErr
		}
		if embedding.IsRetryable(embedErr) {
			unavailable.Reason = semanticindex.ReasonProviderUnavailable
		} else {
			unavailable.Reason = semanticindex.ReasonQueryEmbeddingFailed
		}
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
	hits, status, searchErr := r.searcher.Search(ctx, queryVector, semanticindex.SearchOptions{
		ProfileID: profileID, Dimensions: opts.Profile.Dimensions, Limit: opts.Limit,
		MaxChunks: opts.MaxChunks, Filters: opts.Filters,
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
