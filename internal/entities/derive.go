package entities

import (
	"strings"

	"github.com/darron/dbrain/internal/model"
)

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
