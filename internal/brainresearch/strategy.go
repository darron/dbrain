package brainresearch

import (
	"context"
	"strings"

	"github.com/darron/dbrain/internal/ask"
)

func buildResearchStrategy(question string, hints ask.QueryHints) researchStrategy {
	return buildDeterministicResearchStrategy(question, hints)
}

func (b *Builder) buildResearchStrategy(ctx context.Context, question string, hints ask.QueryHints, opts Options) researchStrategy {
	strategy := buildDeterministicResearchStrategy(question, hints)
	strategy.Planner = "deterministic"
	if opts.DisablePlanner {
		return strategy
	}
	if !opts.UseModelPlanner && strings.TrimSpace(opts.PlannerModel) == "" {
		return strategy
	}

	planned, modelName, err := b.buildModelResearchPlan(ctx, question, hints, strategy, opts)
	if modelName != "" {
		strategy.PlannerModel = modelName
	}
	if err != nil {
		strategy.PlannerError = err.Error()
		return strategy
	}
	if planned.Empty() {
		return strategy
	}

	strategy.Planner = "model_assisted"
	strategy.Concepts = mergeQueryConcepts(strategy.Concepts, planned.Concepts)
	strategy.Variants = mergeQueryVariants(strategy.Variants, planned.QueryVariants)
	if preferred := preferredConceptQuery(strategy.Concepts); preferred != "" {
		strategy.Variants = mergeQueryVariants(strategy.Variants, []QueryVariant{{Query: preferred, Reason: "model_concept_terms"}})
	}
	strategy.Variants = limitQueryVariants(strategy.Variants)
	return strategy
}

func buildDeterministicResearchStrategy(question string, hints ask.QueryHints) researchStrategy {
	terms := uniqueStrings(hints.Terms)
	concepts := buildQueryConcepts(terms)
	variants := buildQueryVariants(question, hints.TextQuery, terms, concepts)
	return researchStrategy{Variants: variants, Concepts: concepts, Planner: "deterministic"}
}
