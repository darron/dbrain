package entities

import (
	"net/url"
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

func xAuthorIdentityTokens(b *builder) []string {
	values := []string{
		xAuthorHandleFromKey(b.entity.Key),
		b.entity.Name,
	}
	for alias := range b.aliases {
		values = append(values, alias)
	}

	seen := map[string]struct{}{}
	out := make([]string, 0, len(values))
	for _, value := range values {
		value = strings.TrimSpace(strings.TrimPrefix(value, "@"))
		if value == "" {
			continue
		}
		if strings.ContainsAny(value, " \t") {
			continue
		}
		token := normalizeIdentityToken(value)
		if token == "" {
			continue
		}
		if _, exists := seen[token]; exists {
			continue
		}
		seen[token] = struct{}{}
		out = append(out, token)
	}
	return out
}

func generalIdentityTokens(b *builder) []string {
	values := []string{
		b.entity.Name,
		b.entity.CanonicalURL,
		entityKeyValue(b.entity.Key),
	}
	if !isGenericIdentityHost(b.entity.Domain) {
		values = append(values, b.entity.Domain)
	}
	for alias := range b.aliases {
		values = append(values, alias)
	}

	seen := map[string]struct{}{}
	out := make([]string, 0, len(values)*2)
	for _, value := range values {
		for _, token := range splitIdentityTokens(value) {
			if token == "" {
				continue
			}
			if _, exists := seen[token]; exists {
				continue
			}
			seen[token] = struct{}{}
			out = append(out, token)
		}
	}
	return out
}

func splitIdentityTokens(value string) []string {
	value = strings.TrimSpace(value)
	if value == "" {
		return nil
	}

	parts := make([]string, 0, 8)
	if looksLikeDomain(value) {
		host := normalizeDomain(value)
		if isGenericIdentityHost(host) {
			return nil
		}
		parts = append(parts, host)
		parts = append(parts, domainBrandToken(host))
		value = ""
	} else if parsed, err := url.Parse(value); err == nil {
		host := normalizeDomain(parsed.Hostname())
		if host != "" && !isGenericIdentityHost(host) {
			parts = append(parts, host)
			parts = append(parts, domainBrandToken(host))
		}
		value = parsed.Path
	}

	replacer := strings.NewReplacer("@", " ", "/", " ", "_", " ", "-", " ", ".", " ", ":", " ")
	parts = append(parts, strings.Fields(replacer.Replace(value))...)

	seen := map[string]struct{}{}
	out := make([]string, 0, len(parts))
	for _, part := range parts {
		token := normalizeIdentityToken(part)
		if token == "" {
			continue
		}
		if _, exists := seen[token]; exists {
			continue
		}
		seen[token] = struct{}{}
		out = append(out, token)
	}
	return out
}

func looksLikeDomain(value string) bool {
	value = strings.TrimSpace(value)
	if value == "" {
		return false
	}
	if strings.Contains(value, "://") || strings.ContainsAny(value, "/:@ ") {
		return false
	}
	return strings.Contains(value, ".")
}

func normalizeIdentityToken(value string) string {
	value = strings.ToLower(strings.TrimSpace(value))
	if value == "" {
		return ""
	}
	var b strings.Builder
	for _, r := range value {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') {
			b.WriteRune(r)
		}
	}
	token := b.String()
	if len(token) < 2 {
		return ""
	}
	return token
}

func xAuthorHandleFromKey(key string) string {
	if strings.HasPrefix(key, "x-author:name:") {
		return ""
	}
	if strings.HasPrefix(key, "x-author:") {
		return strings.TrimPrefix(key, "x-author:")
	}
	return ""
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

func entityKeyValue(key string) string {
	key = strings.TrimSpace(strings.ToLower(key))
	switch {
	case strings.HasPrefix(key, "github-repo:"):
		return strings.TrimPrefix(key, "github-repo:")
	case strings.HasPrefix(key, "github-owner:"):
		return strings.TrimPrefix(key, "github-owner:")
	case strings.HasPrefix(key, "x-author:name:"):
		return strings.TrimPrefix(key, "x-author:name:")
	case strings.HasPrefix(key, "x-author:"):
		return strings.TrimPrefix(key, "x-author:")
	case strings.HasPrefix(key, "site:"):
		return strings.TrimPrefix(key, "site:")
	default:
		if idx := strings.IndexByte(key, ':'); idx >= 0 && idx+1 < len(key) {
			return key[idx+1:]
		}
		return key
	}
}

func shouldDeriveXAuthorEntity(sourceType string) bool {
	sourceType = strings.ToLower(strings.TrimSpace(sourceType))
	return strings.HasPrefix(sourceType, "x_")
}

func projectOwnerToken(key string) string {
	value := entityKeyValue(key)
	if value == "" {
		return ""
	}
	owner, _, ok := strings.Cut(value, "/")
	if !ok {
		return ""
	}
	return normalizeIdentityToken(owner)
}
