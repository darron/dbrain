package entities

import (
	"strings"
)

func augmentEntityRelationships(builders map[string]*builder) {
	if len(builders) == 0 {
		return
	}

	nonPersonByToken := map[string][]*builder{}
	for _, candidate := range builders {
		if candidate.entity.Kind == KindPerson {
			continue
		}
		for _, token := range generalIdentityTokens(candidate) {
			if len(token) < 4 {
				continue
			}
			nonPersonByToken[token] = appendBuilderUnique(nonPersonByToken[token], candidate)
		}
	}

	for _, candidate := range builders {
		if !strings.HasPrefix(candidate.entity.Key, "x-author:") || candidate.entity.Kind != KindPerson {
			continue
		}
		authorTokens := tokenSet(xAuthorIdentityTokens(candidate))
		matched := map[string]struct{}{}
		for token := range authorTokens {
			if len(token) < 4 {
				continue
			}
			for _, target := range nonPersonByToken[token] {
				if _, exists := matched[target.entity.Key]; exists {
					continue
				}
				if !shouldLinkXAuthorToEntity(target, authorTokens) {
					continue
				}
				matched[target.entity.Key] = struct{}{}
				candidate.addLink(Link{
					Key:          target.entity.Key,
					Name:         target.entity.Name,
					Kind:         target.entity.Kind,
					NotePath:     target.entity.NotePath,
					Relationship: relationshipToCanonicalEntity(target.entity.Kind),
				})
				target.addLink(Link{
					Key:          candidate.entity.Key,
					Name:         candidate.entity.Name,
					Kind:         candidate.entity.Kind,
					NotePath:     candidate.entity.NotePath,
					Relationship: "represented_on_x",
				})
			}
		}
	}
}

func appendBuilderUnique(values []*builder, candidate *builder) []*builder {
	for _, existing := range values {
		if existing == candidate {
			return values
		}
	}
	return append(values, candidate)
}

func tokenSet(values []string) map[string]struct{} {
	set := make(map[string]struct{}, len(values))
	for _, value := range values {
		if value == "" {
			continue
		}
		set[value] = struct{}{}
	}
	return set
}

func shouldLinkXAuthorToEntity(target *builder, authorTokens map[string]struct{}) bool {
	switch target.entity.Kind {
	case KindProject:
		ownerToken := projectOwnerToken(target.entity.Key)
		if ownerToken == "" {
			return false
		}
		_, ok := authorTokens[ownerToken]
		return ok
	default:
		return true
	}
}

func relationshipToCanonicalEntity(kind Kind) string {
	switch kind {
	case KindOrg:
		return "likely_brand_account"
	case KindProject:
		return "likely_project_account"
	case KindSite:
		return "likely_site_account"
	default:
		return "likely_same_identity"
	}
}
