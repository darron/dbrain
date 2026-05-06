package ask

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/darron/dbrain/internal/config"
	"github.com/darron/dbrain/internal/entities"
	"github.com/darron/dbrain/internal/store"
	"github.com/darron/dbrain/internal/summarizecli"
	"github.com/darron/dbrain/internal/summaryconfig"
)

func Run(ctx context.Context, cfg config.Config, st *store.Store, question string, opts Options) (Response, error) {
	opts.Model = summaryconfig.Model(cfg.RootDir, opts.Model)
	question = strings.TrimSpace(question)
	if question == "" {
		return Response{}, fmt.Errorf("question cannot be empty")
	}
	searchQuestion := SearchText(question)
	if searchQuestion == "" {
		searchQuestion = question
	}
	if opts.Limit <= 0 {
		opts.Limit = 8
	}
	if opts.MaxCharsPerDoc <= 0 {
		opts.MaxCharsPerDoc = defaultMaxCharsPerDoc
	}
	if opts.Timeout <= 0 {
		opts.Timeout = 2 * time.Minute
	}
	if strings.TrimSpace(opts.Length) == "" {
		opts.Length = "medium"
	}
	if opts.RelatedLimit < 0 {
		opts.RelatedLimit = 0
	}

	hints := Hints(question)
	searchTerms := hints.Terms
	searchLimit := opts.SearchLimit
	if searchLimit <= 0 {
		searchLimit = opts.Limit * 4
	}
	if searchLimit < opts.Limit {
		searchLimit = opts.Limit
	}
	if searchLimit < 12 {
		searchLimit = 12
	}

	results, err := st.Search(ctx, hints.TextQuery, searchLimit)
	if err != nil {
		return Response{}, err
	}
	sourceResults, err := st.SearchSources(ctx, hints.TextQuery, searchLimit)
	if err != nil {
		return Response{}, err
	}
	results = append(results, sourceResults...)
	if !opts.DisableTagExpansion {
		for _, tagQuery := range hints.TagQueries {
			tagResults, err := st.SearchUserTags(ctx, tagQuery, searchLimit)
			if err != nil {
				return Response{}, err
			}
			results = append(results, tagResults...)
			exactTagResults, err := st.SearchExactUserTag(ctx, tagQuery, searchLimit)
			if err != nil {
				return Response{}, err
			}
			results = append(results, exactTagResults...)
		}
	}

	var entityMatches map[string]entityMatch
	if !opts.DisableEntityExpansion {
		entityIndex := opts.EntityIndex
		if entityIndex == nil {
			var err error
			entityIndex, err = entities.BuildIndex(ctx, st)
			if err != nil {
				return Response{}, err
			}
		}
		entityMatches = buildEntityMatchIndex(entityIndex, searchQuestion, searchTerms, maxInt(opts.Limit*3, 12))
	}

	candidates := make([]evidenceCandidate, 0, len(results))
	seen := map[string]struct{}{}
	for _, result := range results {
		candidate, ok, err := buildEvidence(ctx, cfg, st, result, opts.MaxCharsPerDoc, searchTerms)
		if err != nil {
			return Response{}, err
		}
		if !ok || !matchesSourceTypes(opts.SourceTypes, candidate.SourceType) {
			continue
		}
		if _, exists := seen[candidate.SourceKey]; exists {
			continue
		}
		seen[candidate.SourceKey] = struct{}{}
		scoreCandidate(&candidate, searchQuestion, searchTerms)
		applyEntityMatches(&candidate, entityMatches)
		candidates = append(candidates, candidate)
	}
	entityCandidates, err := collectEntityCandidates(ctx, cfg, st, opts, searchQuestion, searchTerms, entityMatches, seen)
	if err != nil {
		return Response{}, err
	}
	candidates = append(candidates, entityCandidates...)
	rankCandidates(candidates)

	response := Response{
		Question: question,
		Evidence: make([]Evidence, 0, opts.Limit),
	}
	selected := make([]evidenceCandidate, 0, opts.Limit)

	relatedTarget := 0
	if opts.IncludeRelated {
		relatedTarget = opts.RelatedLimit
		if relatedTarget == 0 {
			relatedTarget = 2
		}
		if relatedTarget >= opts.Limit {
			relatedTarget = opts.Limit - 1
		}
	}
	primaryTarget := opts.Limit - relatedTarget
	if primaryTarget <= 0 {
		primaryTarget = opts.Limit
	}

	seen = map[string]struct{}{}
	for _, candidate := range candidates {
		if len(selected) >= primaryTarget {
			break
		}
		if _, exists := seen[candidate.SourceKey]; exists {
			continue
		}
		seen[candidate.SourceKey] = struct{}{}
		selected = append(selected, candidate)
	}

	if opts.IncludeRelated && len(selected) > 0 && len(selected) < opts.Limit {
		related, err := collectRelatedEvidence(ctx, cfg, st, selected, question, searchTerms, opts, seen, entityMatches)
		if err != nil {
			return Response{}, err
		}
		for _, candidate := range related {
			if len(selected) >= opts.Limit {
				break
			}
			if _, exists := seen[candidate.SourceKey]; exists {
				continue
			}
			seen[candidate.SourceKey] = struct{}{}
			selected = append(selected, candidate)
		}
	}

	for _, candidate := range selected {
		response.Evidence = append(response.Evidence, candidate.Evidence)
	}

	if opts.RetrieveOnly || len(response.Evidence) == 0 {
		return response, nil
	}

	inputPath, cleanup, err := writePromptInput(cfg, question, response.Evidence)
	if err != nil {
		return Response{}, err
	}
	defer cleanup()

	runResult, err := summarizecli.Run(ctx, summarizecli.Options{
		Binary:    opts.Binary,
		Input:     inputPath,
		Summarize: true,
		Model:     opts.Model,
		CLI:       askCLI(opts),
		Prompt:    answerPrompt,
		Length:    opts.Length,
		Timeout:   opts.Timeout,
		RootDir:   cfg.RootDir,
	})
	if err != nil {
		return Response{}, err
	}
	if strings.TrimSpace(runResult.Summary.Text) == "" {
		return Response{}, fmt.Errorf("ask returned no answer text")
	}

	response.Answer = strings.TrimSpace(runResult.Summary.Text)
	return response, nil
}
