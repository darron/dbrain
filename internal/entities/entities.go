package entities

import (
	"context"
	"fmt"
	"net/url"
	"path/filepath"
	"sort"
	"strings"

	"dbrain/internal/model"
	"dbrain/internal/store"
)

type Kind string

const (
	KindPerson  Kind = "person"
	KindOrg     Kind = "org"
	KindProject Kind = "project"
	KindSite    Kind = "site"
)

type Reference struct {
	RefKind      string `json:"ref_kind"`
	SourceKey    string `json:"source_key"`
	Title        string `json:"title"`
	NotePath     string `json:"note_path"`
	URL          string `json:"url"`
	SourceType   string `json:"source_type"`
	Relationship string `json:"relationship"`
}

type Link struct {
	Key          string `json:"key"`
	Name         string `json:"name"`
	Kind         Kind   `json:"kind"`
	NotePath     string `json:"note_path"`
	Relationship string `json:"relationship"`
}

type Entity struct {
	Key            string      `json:"key"`
	Name           string      `json:"name"`
	Kind           Kind        `json:"kind"`
	Aliases        []string    `json:"aliases,omitempty"`
	CanonicalURL   string      `json:"canonical_url,omitempty"`
	Domain         string      `json:"domain,omitempty"`
	NotePath       string      `json:"note_path"`
	SourceTypes    []string    `json:"source_types,omitempty"`
	ReferenceCount int         `json:"reference_count"`
	References     []Reference `json:"references,omitempty"`
	Links          []Link      `json:"links,omitempty"`
}

type SearchOptions struct {
	Kind  string
	Limit int
}

type builder struct {
	entity          Entity
	aliases         map[string]struct{}
	sourceTypes     map[string]struct{}
	references      map[string]Reference
	links           map[string]Link
	referenceCounts map[string]struct{}
}

func BuildIndex(ctx context.Context, st *store.Store) ([]Entity, error) {
	items, err := st.ListAllItems(ctx, 0)
	if err != nil {
		return nil, err
	}
	sources, err := st.ListAllSources(ctx, 0)
	if err != nil {
		return nil, err
	}

	builders := map[string]*builder{}

	for _, item := range items {
		addItemEntities(builders, item)
	}
	for _, source := range sources {
		addSourceEntities(builders, source)
	}
	augmentEntityRelationships(builders)

	entities := make([]Entity, 0, len(builders))
	for _, b := range builders {
		entity := b.entity
		entity.Aliases = sortedKeys(b.aliases)
		entity.SourceTypes = sortedKeys(b.sourceTypes)
		entity.ReferenceCount = len(b.referenceCounts)
		entity.References = sortedReferences(b.references)
		entity.Links = sortedLinks(b.links)
		entities = append(entities, entity)
	}

	sort.Slice(entities, func(i, j int) bool {
		if entities[i].ReferenceCount != entities[j].ReferenceCount {
			return entities[i].ReferenceCount > entities[j].ReferenceCount
		}
		if entities[i].Kind != entities[j].Kind {
			return entities[i].Kind < entities[j].Kind
		}
		return strings.ToLower(entities[i].Name) < strings.ToLower(entities[j].Name)
	})
	return entities, nil
}

func Search(ctx context.Context, st *store.Store, query string, opts SearchOptions) ([]Entity, error) {
	entities, err := BuildIndex(ctx, st)
	if err != nil {
		return nil, err
	}
	return Filter(entities, query, opts), nil
}

func Filter(entities []Entity, query string, opts SearchOptions) []Entity {
	kind := normalizeKind(opts.Kind)
	query = strings.ToLower(strings.TrimSpace(query))

	filtered := make([]Entity, 0, len(entities))
	for _, entity := range entities {
		if kind != "" && string(entity.Kind) != kind {
			continue
		}
		if query == "" || entityMatches(entity, query) {
			filtered = append(filtered, entity)
		}
	}

	if opts.Limit > 0 && len(filtered) > opts.Limit {
		filtered = filtered[:opts.Limit]
	}
	return filtered
}

func FormatText(entities []Entity) string {
	if len(entities) == 0 {
		return "Entities: 0"
	}

	var b strings.Builder
	b.WriteString("Entities:\n")
	for _, entity := range entities {
		_, _ = fmt.Fprintf(&b, "- [%s] %s (%s) refs=%d", entity.Kind, entity.Name, entity.Key, entity.ReferenceCount)
		if entity.NotePath != "" {
			_, _ = fmt.Fprintf(&b, " note=%s", entity.NotePath)
		}
		b.WriteString("\n")
	}
	return strings.TrimSpace(b.String())
}

func NoteRelativePath(kind Kind, key string) string {
	return filepath.ToSlash(filepath.Join("entities", string(kind), entitySlug(key)+".md"))
}

func addItemEntities(builders map[string]*builder, item model.Item) {
	if !shouldDeriveXAuthorEntity(item.SourceType) {
		return
	}

	handle := strings.TrimSpace(item.AuthorHandle)
	name := strings.TrimSpace(item.AuthorName)
	if handle == "" && name == "" {
		return
	}

	normalizedHandle := strings.ToLower(handle)
	key := "x-author:" + normalizedHandle
	if handle == "" {
		key = "x-author:name:" + entitySlug(name)
	}
	display := name
	if display == "" {
		display = "@" + normalizedHandle
	}
	if display == "" {
		display = normalizedHandle
	}

	canonical := ""
	if normalizedHandle != "" {
		canonical = "https://x.com/" + normalizedHandle
	}

	entity := ensureBuilder(builders, Entity{
		Key:          key,
		Name:         display,
		Kind:         KindPerson,
		CanonicalURL: canonical,
		NotePath:     NoteRelativePath(KindPerson, key),
	})
	entity.addAlias(name)
	if normalizedHandle != "" {
		entity.addAlias(handle)
		entity.addAlias(normalizedHandle)
		entity.addAlias("@" + handle)
		entity.addAlias("@" + normalizedHandle)
	}
	entity.addSourceType(item.SourceType)
	entity.addReference(Reference{
		RefKind:      "item",
		SourceKey:    item.SourceKey,
		Title:        firstNonEmpty(item.Title, display, item.SourceKey),
		NotePath:     item.NotePath,
		URL:          item.CanonicalURL,
		SourceType:   item.SourceType,
		Relationship: "authored",
	})
}

func addSourceEntities(builders map[string]*builder, source model.SourceDocument) {
	if owner, repo, ok := parseGitHubRepo(source.CanonicalURL); ok {
		ownerKey := "github-owner:" + owner
		repoKey := "github-repo:" + owner + "/" + repo

		ownerEntity := ensureBuilder(builders, Entity{
			Key:          ownerKey,
			Name:         owner,
			Kind:         KindOrg,
			CanonicalURL: "https://github.com/" + owner,
			Domain:       "github.com",
			NotePath:     NoteRelativePath(KindOrg, ownerKey),
		})
		ownerEntity.addAlias(owner)
		ownerEntity.addSourceType(source.SourceType)
		ownerEntity.addReference(Reference{
			RefKind:      "source",
			SourceKey:    source.SourceKey,
			Title:        firstNonEmpty(source.Title, owner+"/"+repo, source.SourceKey),
			NotePath:     source.NotePath,
			URL:          source.CanonicalURL,
			SourceType:   source.SourceType,
			Relationship: "owns",
		})
		ownerEntity.addLink(Link{
			Key:          repoKey,
			Name:         owner + "/" + repo,
			Kind:         KindProject,
			NotePath:     NoteRelativePath(KindProject, repoKey),
			Relationship: "owns",
		})

		repoEntity := ensureBuilder(builders, Entity{
			Key:          repoKey,
			Name:         owner + "/" + repo,
			Kind:         KindProject,
			CanonicalURL: source.CanonicalURL,
			Domain:       source.Domain,
			NotePath:     NoteRelativePath(KindProject, repoKey),
		})
		repoEntity.addAlias(repo)
		repoEntity.addAlias(owner + "/" + repo)
		repoEntity.addSourceType(source.SourceType)
		repoEntity.addReference(Reference{
			RefKind:      "source",
			SourceKey:    source.SourceKey,
			Title:        firstNonEmpty(source.Title, owner+"/"+repo, source.SourceKey),
			NotePath:     source.NotePath,
			URL:          source.CanonicalURL,
			SourceType:   source.SourceType,
			Relationship: "described_by",
		})
		repoEntity.addLink(Link{
			Key:          ownerKey,
			Name:         owner,
			Kind:         KindOrg,
			NotePath:     NoteRelativePath(KindOrg, ownerKey),
			Relationship: "owned_by",
		})
	}

	domain := normalizeDomain(source.Domain)
	if domain == "" || isGenericSiteDomain(domain) {
		return
	}

	key := "site:" + domain
	name := firstNonEmpty(source.SiteName, domain)
	siteEntity := ensureBuilder(builders, Entity{
		Key:          key,
		Name:         name,
		Kind:         KindSite,
		CanonicalURL: siteRootURL(source.CanonicalURL, domain),
		Domain:       domain,
		NotePath:     NoteRelativePath(KindSite, key),
	})
	siteEntity.addAlias(source.SiteName)
	siteEntity.addAlias(domain)
	siteEntity.addAlias(brandTokenFromURL(source.CanonicalURL))
	siteEntity.addSourceType(source.SourceType)
	siteEntity.addReference(Reference{
		RefKind:      "source",
		SourceKey:    source.SourceKey,
		Title:        firstNonEmpty(source.Title, name, source.SourceKey),
		NotePath:     source.NotePath,
		URL:          source.CanonicalURL,
		SourceType:   source.SourceType,
		Relationship: "published_on",
	})
}

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

func ensureBuilder(builders map[string]*builder, entity Entity) *builder {
	if existing, ok := builders[entity.Key]; ok {
		if existing.entity.Name == "" && entity.Name != "" {
			existing.entity.Name = entity.Name
		}
		if existing.entity.CanonicalURL == "" && entity.CanonicalURL != "" {
			existing.entity.CanonicalURL = entity.CanonicalURL
		}
		if existing.entity.Domain == "" && entity.Domain != "" {
			existing.entity.Domain = entity.Domain
		}
		if existing.entity.NotePath == "" && entity.NotePath != "" {
			existing.entity.NotePath = entity.NotePath
		}
		return existing
	}

	b := &builder{
		entity:          entity,
		aliases:         map[string]struct{}{},
		sourceTypes:     map[string]struct{}{},
		references:      map[string]Reference{},
		links:           map[string]Link{},
		referenceCounts: map[string]struct{}{},
	}
	builders[entity.Key] = b
	return b
}

func (b *builder) addAlias(value string) {
	value = strings.TrimSpace(value)
	if value == "" || strings.EqualFold(value, b.entity.Name) {
		return
	}
	b.aliases[value] = struct{}{}
}

func (b *builder) addSourceType(value string) {
	value = strings.TrimSpace(value)
	if value == "" {
		return
	}
	b.sourceTypes[value] = struct{}{}
}

func (b *builder) addReference(ref Reference) {
	if strings.TrimSpace(ref.SourceKey) == "" {
		return
	}
	ref.Title = strings.TrimSpace(ref.Title)
	key := ref.RefKind + "|" + ref.SourceKey + "|" + ref.Relationship
	b.references[key] = ref
	b.referenceCounts[ref.RefKind+"|"+ref.SourceKey] = struct{}{}
}

func (b *builder) addLink(link Link) {
	if strings.TrimSpace(link.Key) == "" {
		return
	}
	key := string(link.Kind) + "|" + link.Key + "|" + link.Relationship
	b.links[key] = link
}

func sortedKeys(values map[string]struct{}) []string {
	result := make([]string, 0, len(values))
	for value := range values {
		result = append(result, value)
	}
	sort.Slice(result, func(i, j int) bool {
		return strings.ToLower(result[i]) < strings.ToLower(result[j])
	})
	return result
}

func sortedReferences(values map[string]Reference) []Reference {
	result := make([]Reference, 0, len(values))
	for _, value := range values {
		result = append(result, value)
	}
	sort.Slice(result, func(i, j int) bool {
		if result[i].Relationship != result[j].Relationship {
			return result[i].Relationship < result[j].Relationship
		}
		return strings.ToLower(result[i].Title) < strings.ToLower(result[j].Title)
	})
	return result
}

func sortedLinks(values map[string]Link) []Link {
	result := make([]Link, 0, len(values))
	for _, value := range values {
		result = append(result, value)
	}
	sort.Slice(result, func(i, j int) bool {
		if result[i].Kind != result[j].Kind {
			return result[i].Kind < result[j].Kind
		}
		if result[i].Relationship != result[j].Relationship {
			return result[i].Relationship < result[j].Relationship
		}
		return strings.ToLower(result[i].Name) < strings.ToLower(result[j].Name)
	})
	return result
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

func entityMatches(entity Entity, query string) bool {
	candidates := []string{entity.Key, entity.Name, entity.CanonicalURL, entity.Domain}
	candidates = append(candidates, entity.Aliases...)
	for _, candidate := range candidates {
		if strings.Contains(strings.ToLower(strings.TrimSpace(candidate)), query) {
			return true
		}
	}
	return false
}

func normalizeKind(value string) string {
	value = strings.ToLower(strings.TrimSpace(value))
	switch value {
	case "person", "org", "project", "site":
		return value
	default:
		return ""
	}
}

func parseGitHubRepo(raw string) (string, string, bool) {
	u, err := url.Parse(strings.TrimSpace(raw))
	if err != nil {
		return "", "", false
	}
	host := normalizeDomain(u.Hostname())
	if host != "github.com" {
		return "", "", false
	}
	parts := splitPath(u.Path)
	if len(parts) < 2 {
		return "", "", false
	}
	return parts[0], parts[1], true
}

func splitPath(path string) []string {
	raw := strings.Split(strings.Trim(path, "/"), "/")
	result := make([]string, 0, len(raw))
	for _, part := range raw {
		part = strings.TrimSpace(part)
		if part != "" {
			result = append(result, part)
		}
	}
	return result
}

func brandTokenFromURL(raw string) string {
	u, err := url.Parse(strings.TrimSpace(raw))
	if err != nil {
		return ""
	}
	return domainBrandToken(u.Hostname())
}

func normalizeDomain(value string) string {
	value = strings.ToLower(strings.TrimSpace(value))
	value = strings.TrimPrefix(value, "www.")
	return value
}

func domainBrandToken(value string) string {
	domain := normalizeDomain(value)
	if domain == "" {
		return ""
	}
	if isGenericIdentityHost(domain) {
		return ""
	}
	parts := strings.Split(domain, ".")
	if len(parts) == 0 {
		return ""
	}
	if len(parts) >= 3 && strings.HasSuffix(domain, ".github.io") {
		return normalizeIdentityToken(parts[len(parts)-3])
	}
	if len(parts) == 1 {
		return normalizeIdentityToken(parts[0])
	}

	idx := len(parts) - 2
	if idx > 0 && isGenericDomainLabel(parts[idx]) {
		idx--
	}
	if idx < 0 || idx >= len(parts) {
		return ""
	}
	return normalizeIdentityToken(parts[idx])
}

func isGenericDomainLabel(value string) bool {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "co", "com", "net", "org", "gov", "edu", "ac":
		return true
	default:
		return false
	}
}

func isGenericIdentityHost(domain string) bool {
	switch normalizeDomain(domain) {
	case "", "github.com", "github.io", "x.com", "twitter.com", "youtube.com", "youtu.be":
		return true
	default:
		return false
	}
}

func isGenericSiteDomain(domain string) bool {
	switch normalizeDomain(domain) {
	case "", "github.com", "x.com", "twitter.com", "youtube.com", "youtu.be":
		return true
	default:
		return false
	}
}

func siteRootURL(raw, domain string) string {
	u, err := url.Parse(strings.TrimSpace(raw))
	if err != nil {
		return "https://" + domain
	}
	scheme := strings.TrimSpace(u.Scheme)
	if scheme == "" {
		scheme = "https"
	}
	return scheme + "://" + domain
}

func entitySlug(value string) string {
	value = strings.ToLower(strings.TrimSpace(value))
	var b strings.Builder
	lastDash := false
	for _, r := range value {
		switch {
		case r >= 'a' && r <= 'z':
			b.WriteRune(r)
			lastDash = false
		case r >= '0' && r <= '9':
			b.WriteRune(r)
			lastDash = false
		default:
			if !lastDash && b.Len() > 0 {
				b.WriteRune('-')
				lastDash = true
			}
		}
	}
	slug := strings.Trim(b.String(), "-")
	if slug == "" {
		return "entity"
	}
	return slug
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if trimmed := strings.TrimSpace(value); trimmed != "" {
			return trimmed
		}
	}
	return ""
}
