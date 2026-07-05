package brainresearch

import (
	"context"
	"strings"
	"time"

	"github.com/darron/dbrain/internal/entities"
	"github.com/darron/dbrain/internal/store"
)

const anchorResolveTimeout = 2 * time.Second

type storeAnchorResolver struct {
	st *store.Store
}

func (r storeAnchorResolver) ResolveAnchors(ctx context.Context, anchors []ProtectedAnchor) ([]ProtectedAnchor, error) {
	if r.st == nil || len(anchors) == 0 {
		return anchors, nil
	}
	index, err := entities.BuildIndex(ctx, r.st)
	if err != nil {
		return nil, err
	}
	out := make([]ProtectedAnchor, 0, len(anchors))
	for _, anchor := range anchors {
		out = append(out, resolveAnchorFromEntities(anchor, index))
	}
	return dedupeProtectedAnchors(out), nil
}

func (b *Builder) resolveProtectedAnchors(ctx context.Context, anchors []ProtectedAnchor, opts Options) []ProtectedAnchor {
	anchors = dedupeProtectedAnchors(anchors)
	if len(anchors) == 0 {
		return nil
	}
	resolver := opts.AnchorResolver
	if resolver == nil {
		resolver = storeAnchorResolver{st: b.st}
	}
	resolveCtx, cancel := context.WithTimeout(ctx, anchorResolveTimeout)
	defer cancel()
	resolved, err := resolver.ResolveAnchors(resolveCtx, anchors)
	if err != nil {
		emitEvent(opts.Observer, "protected_anchors_resolved", map[string]interface{}{
			"anchors": anchors,
			"error":   err.Error(),
		})
		return anchors
	}
	resolved = dedupeProtectedAnchors(resolved)
	if len(resolved) == 0 {
		resolved = anchors
	}
	emitEvent(opts.Observer, "protected_anchors_resolved", map[string]interface{}{
		"anchors": resolved,
	})
	return resolved
}

func resolveAnchorFromEntities(anchor ProtectedAnchor, index []entities.Entity) ProtectedAnchor {
	anchor = normalizeProtectedAnchor(anchor)
	if anchor.Kind == "source_key" || anchor.ResolvedID != "" {
		return anchor
	}
	values := normalizedAnchorLookupValues(anchor)
	for _, entity := range index {
		if !entityMatchesAnchor(entity, values) {
			continue
		}
		return enrichAnchorFromEntity(anchor, entity)
	}
	return anchor
}

func entityMatchesAnchor(entity entities.Entity, values []string) bool {
	candidates := []string{entity.Key, entity.Name, entity.CanonicalURL, entity.Domain}
	candidates = append(candidates, entity.Aliases...)
	for _, candidate := range candidates {
		candidateValues := []string{strings.ToLower(strings.TrimSpace(candidate)), normalizeAnchorToken(candidate)}
		for _, left := range values {
			for _, right := range candidateValues {
				if left != "" && right != "" && left == right {
					return true
				}
			}
		}
	}
	return false
}

func enrichAnchorFromEntity(anchor ProtectedAnchor, entity entities.Entity) ProtectedAnchor {
	anchor.ResolvedID = entity.Key
	if strings.HasPrefix(entity.Key, "x-author:") {
		anchor.Kind = "handle"
		anchor.Relation = "authored_by"
		handle := strings.TrimPrefix(entity.Key, "x-author:")
		if handle != "" {
			anchor.ExactTerms = append(anchor.ExactTerms, "@"+handle, handle)
		}
	}
	if anchor.Kind == "" {
		anchor.Kind = "entity_alias"
	}
	if anchor.Relation == "" {
		anchor.Relation = "about"
	}
	anchor.Confidence = "alias"
	anchor.ExactTerms = append(anchor.ExactTerms, entity.Key, entity.Name)
	anchor.ExactTerms = append(anchor.ExactTerms, entity.Aliases...)
	anchor.PhraseTerms = append(anchor.PhraseTerms, strings.ToLower(entity.Name))
	for _, alias := range entity.Aliases {
		anchor.PhraseTerms = append(anchor.PhraseTerms, strings.ToLower(strings.NewReplacer("_", " ", "-", " ").Replace(alias)))
	}
	return normalizeProtectedAnchor(anchor)
}
