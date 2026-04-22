package entities

import (
	"context"
	"testing"
	"time"

	"dbrain/internal/model"
	"dbrain/internal/store"
)

func TestBuildIndexDerivesStableEntities(t *testing.T) {
	t.Parallel()

	st, err := store.Open(t.TempDir() + "/brain.db")
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	defer func() { _ = st.Close() }()

	ctx := context.Background()
	now := time.Now().UTC()

	itemResult, err := st.UpsertItem(ctx, model.Item{
		SourceKey:    "x:entity-item",
		SourceType:   "x_bookmark",
		ExternalID:   "entity-item",
		CanonicalURL: "https://x.com/example/status/entity-item",
		Title:        "Entity item",
		AuthorHandle: "alice",
		AuthorName:   "Alice Example",
		Text:         "entity corpus",
		ContentHash:  "item-hash",
		NotePath:     "items/x/2026/entity-item.md",
		RawJSON:      `{}`,
		ImportedAt:   now,
		UpdatedAt:    now,
		LastSeenAt:   now,
	})
	if err != nil {
		t.Fatalf("upsert item: %v", err)
	}

	if _, err := st.UpsertSourceLink(ctx, itemResult.ItemID, model.SourceCandidate{
		SourceKey:     "src:github-entity",
		OriginalURL:   "https://github.com/example/project",
		CanonicalURL:  "https://github.com/example/project",
		NormalizedURL: "https://github.com/example/project",
		SourceType:    "github",
		Domain:        "github.com",
		NotePath:      "sources/github/example-project.md",
	}); err != nil {
		t.Fatalf("upsert github source: %v", err)
	}

	if _, err := st.UpsertSourceLink(ctx, itemResult.ItemID, model.SourceCandidate{
		SourceKey:     "src:web-entity",
		OriginalURL:   "https://example.com/project",
		CanonicalURL:  "https://example.com/project",
		NormalizedURL: "https://example.com/project",
		SourceType:    "web",
		Domain:        "example.com",
		NotePath:      "sources/web/example-project.md",
	}); err != nil {
		t.Fatalf("upsert web source: %v", err)
	}

	brandItemResult, err := st.UpsertItem(ctx, model.Item{
		SourceKey:    "x:entity-brand",
		SourceType:   "x_bookmark",
		ExternalID:   "entity-brand",
		CanonicalURL: "https://x.com/Cloudflare/status/entity-brand",
		Title:        "Cloudflare entity item",
		AuthorHandle: "Cloudflare",
		AuthorName:   "Cloudflare",
		Text:         "brand corpus",
		ContentHash:  "brand-item-hash",
		NotePath:     "items/x/2026/entity-brand.md",
		RawJSON:      `{}`,
		ImportedAt:   now,
		UpdatedAt:    now,
		LastSeenAt:   now,
	})
	if err != nil {
		t.Fatalf("upsert brand item: %v", err)
	}

	if _, err := st.UpsertSourceLink(ctx, brandItemResult.ItemID, model.SourceCandidate{
		SourceKey:     "src:brand-repo",
		OriginalURL:   "https://github.com/cloudflare/pal",
		CanonicalURL:  "https://github.com/cloudflare/pal",
		NormalizedURL: "https://github.com/cloudflare/pal",
		SourceType:    "github",
		Domain:        "github.com",
		NotePath:      "sources/github/cloudflare-pal.md",
	}); err != nil {
		t.Fatalf("upsert first-party repo source: %v", err)
	}

	if _, err := st.UpsertSourceLink(ctx, brandItemResult.ItemID, model.SourceCandidate{
		SourceKey:     "src:third-party-repo",
		OriginalURL:   "https://github.com/adyanth/cloudflare-operator",
		CanonicalURL:  "https://github.com/adyanth/cloudflare-operator",
		NormalizedURL: "https://github.com/adyanth/cloudflare-operator",
		SourceType:    "github",
		Domain:        "github.com",
		NotePath:      "sources/github/adyanth-cloudflare-operator.md",
	}); err != nil {
		t.Fatalf("upsert third-party repo source: %v", err)
	}

	brandLink, err := st.UpsertSourceLink(ctx, brandItemResult.ItemID, model.SourceCandidate{
		SourceKey:     "src:brand-site",
		OriginalURL:   "https://cfl.re/agent-memory",
		CanonicalURL:  "https://blog.cloudflare.com/agent-memory",
		NormalizedURL: "https://blog.cloudflare.com/agent-memory",
		SourceType:    "web",
		Domain:        "cfl.re",
		NotePath:      "sources/web/cloudflare-agent-memory.md",
	})
	if err != nil {
		t.Fatalf("upsert brand source: %v", err)
	}

	if _, err := st.UpsertItem(ctx, model.Item{
		SourceKey:    "gh-star:test:cloudflare/pal",
		SourceType:   "github_star",
		ExternalID:   "cloudflare/pal",
		CanonicalURL: "https://github.com/cloudflare/pal",
		Title:        "cloudflare/pal",
		AuthorHandle: "cloudflare",
		AuthorName:   "cloudflare",
		Text:         "github star corpus",
		ContentHash:  "github-star-hash",
		NotePath:     "items/github/2026/cloudflare__pal.md",
		RawJSON:      `{}`,
		ImportedAt:   now,
		UpdatedAt:    now,
		LastSeenAt:   now,
	}); err != nil {
		t.Fatalf("upsert github star item: %v", err)
	}
	if _, err := st.UpsertItem(ctx, model.Item{
		SourceKey:    "gh-star:test:octocat/hello-world",
		SourceType:   "github_star",
		ExternalID:   "octocat/hello-world",
		CanonicalURL: "https://github.com/octocat/hello-world",
		Title:        "octocat/hello-world",
		AuthorHandle: "octocat",
		AuthorName:   "The Octocat",
		Text:         "another github star corpus",
		ContentHash:  "github-star-hash-octocat",
		NotePath:     "items/github/2026/octocat__hello-world.md",
		RawJSON:      `{}`,
		ImportedAt:   now,
		UpdatedAt:    now,
		LastSeenAt:   now,
	}); err != nil {
		t.Fatalf("upsert github star octocat item: %v", err)
	}
	if _, err := st.SaveSourceExtraction(ctx, brandLink.SourceID, model.ExtractResult{
		CanonicalURL: "https://blog.cloudflare.com/agent-memory",
		FinalURL:     "https://blog.cloudflare.com/agent-memory",
		Title:        "Agent Memory",
		SiteName:     "The Cloudflare Blog",
		Content:      "cloudflare brand site",
		Status:       "ok",
		FetchedAt:    now,
		Tool:         "test",
		ToolVersion:  "test",
	}, "brand-site-hash"); err != nil {
		t.Fatalf("save brand source extraction: %v", err)
	}

	entitiesList, err := BuildIndex(ctx, st)
	if err != nil {
		t.Fatalf("build index: %v", err)
	}

	assertEntity(t, entitiesList, "x-author:alice", KindPerson, "Alice Example")
	assertEntity(t, entitiesList, "github-owner:example", KindOrg, "example")
	project := assertEntity(t, entitiesList, "github-repo:example/project", KindProject, "example/project")
	assertEntity(t, entitiesList, "site:example.com", KindSite, "example.com")
	brandAccount := assertEntity(t, entitiesList, "x-author:cloudflare", KindPerson, "Cloudflare")
	brandSite := assertEntity(t, entitiesList, "site:cfl.re", KindSite, "The Cloudflare Blog")
	assertEntity(t, entitiesList, "github-repo:cloudflare/pal", KindProject, "cloudflare/pal")
	assertMissingEntity(t, entitiesList, "x-author:octocat")

	foundOwnedBy := false
	for _, link := range project.Links {
		if link.Key == "github-owner:example" && link.Relationship == "owned_by" {
			foundOwnedBy = true
			break
		}
	}
	if !foundOwnedBy {
		t.Fatalf("expected project entity to link to github owner, got %+v", project.Links)
	}

	foundBrandLink := false
	for _, link := range brandAccount.Links {
		if link.Key == "site:cfl.re" && link.Relationship == "likely_site_account" {
			foundBrandLink = true
			break
		}
	}
	if !foundBrandLink {
		t.Fatalf("expected x account to link to brand site, got %+v", brandAccount.Links)
	}

	foundFirstPartyRepoLink := false
	foundThirdPartyRepoLink := false
	for _, link := range brandAccount.Links {
		if link.Key == "github-repo:cloudflare/pal" && link.Relationship == "likely_project_account" {
			foundFirstPartyRepoLink = true
		}
		if link.Key == "github-repo:adyanth/cloudflare-operator" && link.Relationship == "likely_project_account" {
			foundThirdPartyRepoLink = true
		}
	}
	if !foundFirstPartyRepoLink {
		t.Fatalf("expected x account to link to first-party repo, got %+v", brandAccount.Links)
	}
	if foundThirdPartyRepoLink {
		t.Fatalf("did not expect x account to link to third-party repo, got %+v", brandAccount.Links)
	}

	foundRepresentedOnX := false
	for _, link := range brandSite.Links {
		if link.Key == "x-author:cloudflare" && link.Relationship == "represented_on_x" {
			foundRepresentedOnX = true
			break
		}
	}
	if !foundRepresentedOnX {
		t.Fatalf("expected site entity to link back to x account, got %+v", brandSite.Links)
	}
}

func TestDomainBrandTokenPrefersHostedSubdomainOverGenericPlatform(t *testing.T) {
	t.Parallel()

	if got := domainBrandToken("cloudflare.github.io"); got != "cloudflare" {
		t.Fatalf("expected hosted subdomain token cloudflare, got %q", got)
	}
	if got := domainBrandToken("github.com"); got != "" {
		t.Fatalf("expected generic github.com to be ignored, got %q", got)
	}
	if got := splitIdentityTokens("github.com"); len(got) != 0 {
		t.Fatalf("expected generic github.com tokens to be ignored, got %#v", got)
	}
}

func assertEntity(t *testing.T, entitiesList []Entity, key string, kind Kind, name string) Entity {
	t.Helper()

	for _, entity := range entitiesList {
		if entity.Key == key {
			if entity.Kind != kind {
				t.Fatalf("expected kind %q for %s, got %q", kind, key, entity.Kind)
			}
			if entity.Name != name {
				t.Fatalf("expected name %q for %s, got %q", name, key, entity.Name)
			}
			if entity.ReferenceCount == 0 {
				t.Fatalf("expected references for %s", key)
			}
			return entity
		}
	}

	t.Fatalf("missing entity %s", key)
	return Entity{}
}

func assertMissingEntity(t *testing.T, entitiesList []Entity, key string) {
	t.Helper()

	for _, entity := range entitiesList {
		if entity.Key == key {
			t.Fatalf("did not expect entity %s, got %+v", key, entity)
		}
	}
}
