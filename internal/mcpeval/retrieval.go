package mcpeval

import (
	"context"

	"github.com/darron/dbrain/internal/ask"
	"github.com/darron/dbrain/internal/config"
	"github.com/darron/dbrain/internal/mcpserver"
	"github.com/darron/dbrain/internal/store"
)

func runRetrievalCase(ctx context.Context, cfg config.Config, st *store.Store, tc Case) (ask.Response, []ask.Evidence, error) {
	if caseNeedsResearchPack(tc) {
		pack, err := mcpserver.New(cfg, st).BuildResearchPack(ctx, mcpserver.ResearchPackOptions{
			Question:       tc.Question,
			Limit:          tc.Limit,
			SourceTypes:    tc.SourceTypes,
			IncludeRelated: tc.IncludeRelated,
			RelatedLimit:   tc.RelatedLimit,
			MaxCharsPerDoc: tc.MaxCharsPerDoc,
		})
		if err != nil {
			return ask.Response{}, nil, err
		}
		return ask.Response{
			Question: pack.Question,
			Evidence: pack.Evidence,
		}, pack.ExactTagEvidence, nil
	}

	response, err := ask.Run(ctx, cfg, st, tc.Question, ask.Options{
		Limit:          tc.Limit,
		RetrieveOnly:   true,
		MaxCharsPerDoc: tc.MaxCharsPerDoc,
		SourceTypes:    tc.SourceTypes,
		IncludeRelated: tc.IncludeRelated,
		RelatedLimit:   tc.RelatedLimit,
	})
	return response, nil, err
}

func caseNeedsResearchPack(tc Case) bool {
	return tc.MinExactTagEvidence > 0 ||
		len(tc.ExpectExactTagEvidenceSourceKeys) > 0 ||
		len(tc.ExpectAnyExactTagEvidenceSourceKeys) > 0 ||
		len(tc.ExpectExactTagEvidenceText) > 0 ||
		len(tc.ForbidExactTagEvidenceText) > 0
}
