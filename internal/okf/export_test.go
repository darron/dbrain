package okf

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/darron/dbrain/internal/config"
	"github.com/darron/dbrain/internal/entities"
	"github.com/darron/dbrain/internal/model"
	"github.com/darron/dbrain/internal/store"
	"github.com/darron/dbrain/internal/topics"
)

func TestBuildBundleDeterministicItemSourceMediaFixture(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, 6, 14, 18, 0, 0, 0, time.UTC)
	item := model.Item{
		ID:                   10,
		SourceKey:            "x:204",
		SourceType:           "x_bookmark",
		ExternalID:           "204",
		CanonicalURL:         "https://x.com/example/status/204",
		Title:                "A title with ] brackets (and parens)",
		AuthorHandle:         "example",
		AuthorName:           "Example Person",
		PublishedAt:          "2026-06-01T10:00:00-06:00",
		SavedAt:              "2026-06-02T10:00:00Z",
		Text:                 "Saved post body.",
		XPostText:            "Canonical post text.",
		SummaryText:          "Derived item summary.",
		SummaryStatus:        "current",
		SummaryModel:         "local/test",
		SummaryPromptVersion: "item-summary-v1",
		OCRText:              "Raw OCR evidence.",
		OCRStatus:            "current",
		ArticleTitle:         model.XMediaTranscriptArticleTitle,
		ArticleText:          "Remote URL: https://video.twimg.com/test.mp4\nLocal Path: `media/x/video/local.mp4`\n\nTranscript:\n\nRaw transcript evidence.",
		UserTags:             "tag-one, tag two",
		NotePath:             "items/x/2026/204.md",
	}
	source := model.SourceDocument{
		ID:                   20,
		SourceKey:            "src:example",
		CanonicalURL:         "https://example.com/page",
		NormalizedURL:        "https://example.com/page",
		SourceType:           "web",
		Domain:               "example.com",
		Title:                "Example Source",
		Description:          "A source description. Second sentence.",
		ExtractedText:        "Raw extracted source text.\n*   Local Path: `media/x/video/source-local.mp4`\n*   Local: `media/x/video/source-short-local.mp4`\nMore raw source text.",
		ExtractStatus:        "current",
		SummaryText:          "Derived source summary.\nLocal Path: `media/x/video/source-summary-local.mp4`",
		SummaryStatus:        "current",
		SummaryModel:         "openrouter/test",
		SummaryPromptVersion: "source-summary-v1",
		NotePath:             "sources/web/example.md",
		CreatedAt:            now,
	}
	snapshot := store.OKFExportSnapshot{
		Items:   []model.Item{item},
		Sources: []model.SourceDocument{source},
		ItemSources: map[int64][]model.ItemSourceRef{
			item.ID: {{
				SourceID:     source.ID,
				SourceKey:    source.SourceKey,
				CanonicalURL: source.CanonicalURL,
				SourceType:   source.SourceType,
				Title:        source.Title,
				NotePath:     source.NotePath,
			}},
		},
		SourceBacklinks: map[int64][]model.SourceBacklink{
			source.ID: {{
				ItemID:       item.ID,
				SourceKey:    item.SourceKey,
				SourceType:   item.SourceType,
				CanonicalURL: item.CanonicalURL,
				Title:        item.Title,
				NotePath:     item.NotePath,
				AuthorHandle: item.AuthorHandle,
				AuthorName:   item.AuthorName,
				PublishedAt:  item.PublishedAt,
			}},
		},
		ItemMedia: map[int64][]model.ItemMediaRef{
			item.ID: {{
				MediaAssetID:   200,
				Ordinal:        0,
				ExpandedURL:    "https://x.com/example/status/204/photo/1",
				RemoteURL:      "https://pbs.twimg.com/media/test.jpg",
				MediaType:      "photo",
				DownloadStatus: "downloaded",
				LocalPath:      "media/x/photo/local.jpg",
				ArchiveStatus:  model.MediaArchiveStatusArchived,
				ArchiveKey:     "media/test.jpg",
				Width:          1200,
				Height:         800,
			}},
		},
		ItemChildren: map[int64][]model.SourceBacklink{},
	}

	opts := ExportOptions{Profile: ProfilePrivate, IncludeItems: true, IncludeSources: true, IncludeRaw: true, MediaPublicBaseURL: "https://cdn.example.com/", Now: now, DbrainVersion: "test"}
	first, err := BuildBundle(snapshot, opts)
	if err != nil {
		t.Fatalf("BuildBundle first: %v", err)
	}
	second, err := BuildBundle(snapshot, opts)
	if err != nil {
		t.Fatalf("BuildBundle second: %v", err)
	}
	firstBytes := renderBundleForTest(t, first)
	secondBytes := renderBundleForTest(t, second)
	if firstBytes != secondBytes {
		t.Fatalf("bundle render not deterministic\nfirst:\n%s\nsecond:\n%s", firstBytes, secondBytes)
	}
	if strings.Contains(firstBytes, "summary_status:") || strings.Contains(firstBytes, "ocr_status:") || strings.Contains(firstBytes, "last_seen_at:") || strings.Contains(firstBytes, "extract_status:") || strings.Contains(firstBytes, "summarized_at:") {
		t.Fatalf("volatile status leaked into frontmatter/body unexpectedly:\n%s", firstBytes)
	}
	if strings.Contains(firstBytes, `site_name: ""`) || strings.Contains(firstBytes, `dbrain_external_id: ""`) {
		t.Fatalf("empty string frontmatter field leaked into OKF output:\n%s", firstBytes)
	}
	for _, want := range []string{
		"generated:\n  by: dbrain/test\n  at: \"2026-06-14T18:00:00Z\"",
		"sources:\n  - resource: https://x.com/example/status/204",
		"type: Item",
		"dbrain_concept_id: item/x%3A204",
		"summary_model: local/test",
		"# Raw Evidence",
		"## Media Transcript",
		"Raw OCR evidence.",
		"Original item: https://x.com/example/status/204",
		"Media source: https://pbs.twimg.com/media/test.jpg",
		"Expanded media URL: https://x.com/example/status/204/photo/1",
		"Archived media: https://cdn.example.com/media/test.jpg",
		"Derived source summary.",
	} {
		if !strings.Contains(firstBytes, want) {
			t.Fatalf("expected rendered bundle to contain %q\n%s", want, firstBytes)
		}
	}
	if strings.Contains(firstBytes, "media/x/photo/local.jpg") {
		t.Fatalf("local media path leaked into OKF output:\n%s", firstBytes)
	}
	if strings.Contains(firstBytes, "media/x/video/local.mp4") {
		t.Fatalf("generated transcript local media path leaked into OKF output:\n%s", firstBytes)
	}
	if strings.Contains(firstBytes, "media/x/video/source-local.mp4") || strings.Contains(firstBytes, "media/x/video/source-short-local.mp4") || strings.Contains(firstBytes, "media/x/video/source-summary-local.mp4") {
		t.Fatalf("source local media path leaked into OKF output:\n%s", firstBytes)
	}
	if strings.Contains(firstBytes, "Local Path:") {
		t.Fatalf("local path metadata leaked into OKF output:\n%s", firstBytes)
	}
	if strings.Contains(firstBytes, "timestamp:") || strings.Contains(firstBytes, "# Citations") {
		t.Fatalf("legacy OKF v0.1 metadata leaked into OKF v0.2 output:\n%s", firstBytes)
	}

	root := t.TempDir()
	if err := writeBundle(root, first); err != nil {
		t.Fatalf("writeBundle: %v", err)
	}
	validation, err := ValidateBundle(root)
	if err != nil {
		t.Fatalf("ValidateBundle: %v", err)
	}
	if !validation.Conformant || validation.BrokenInternalLinks != 0 {
		t.Fatalf("unexpected validation result: %+v", validation)
	}
	index, err := os.ReadFile(filepath.Join(root, "index.md"))
	if err != nil {
		t.Fatalf("read index: %v", err)
	}
	if !strings.HasPrefix(string(index), "---\nokf_version: \"0.2\"\n---\n") {
		t.Fatalf("root index.md must declare OKF v0.2:\n%s", string(index))
	}
	if !strings.Contains(string(index), `A title with \] brackets`) {
		t.Fatalf("index did not escape link text:\n%s", string(index))
	}
}

func TestArchivedMediaURLDerivesProxyFromRootBase(t *testing.T) {
	t.Parallel()

	ref := model.ItemMediaRef{
		MediaAssetID:   42,
		ArchiveStatus:  model.MediaArchiveStatusArchived,
		ArchiveKey:     "media/x/photo/test.jpg",
		ArchiveURL:     "",
		DownloadStatus: model.MediaDownloadStatusDownloaded,
	}
	got := archivedMediaURL(ref, ExportOptions{
		MediaProxyBaseURL: "https://dbrain.example.test/",
	})
	if got != "https://dbrain.example.test/media/asset/42" {
		t.Fatalf("archivedMediaURL() = %q", got)
	}
}

func TestBuildBundleDerivedViewsLinkToExportedConcepts(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, 6, 14, 18, 30, 0, 0, time.UTC)
	item := model.Item{
		ID:           101,
		SourceKey:    "x:derived",
		SourceType:   "x_bookmark",
		CanonicalURL: "https://x.com/example/status/derived",
		Title:        "Derived item",
		PublishedAt:  "2026-06-01T10:00:00Z",
		NotePath:     "items/x/2026/derived.md",
	}
	source := model.SourceDocument{
		ID:           202,
		SourceKey:    "src:derived",
		CanonicalURL: "https://example.com/derived",
		SourceType:   "web",
		Domain:       "example.com",
		Title:        "Derived source",
		NotePath:     "sources/web/derived.md",
		CreatedAt:    now,
	}
	entity := entities.Entity{
		Key:            "site:example.com",
		Name:           "Example",
		Kind:           entities.KindSite,
		Aliases:        []string{"example.com"},
		CanonicalURL:   "https://example.com",
		Domain:         "example.com",
		SourceTypes:    []string{"web", "x_bookmark"},
		ReferenceCount: 2,
		References: []entities.Reference{
			{SourceKey: item.SourceKey, Title: item.Title, SourceType: item.SourceType, Relationship: "mentions"},
			{SourceKey: source.SourceKey, Title: source.Title, SourceType: source.SourceType, Relationship: "canonical"},
		},
	}
	graph := topics.TopicMap{
		Topic:        "derived example",
		SeedLimit:    2,
		RelatedLimit: 1,
		Entities: []topics.TopicEntity{{
			Key:               entity.Key,
			Name:              entity.Name,
			Kind:              string(entity.Kind),
			CanonicalURL:      entity.CanonicalURL,
			ReferenceCount:    entity.ReferenceCount,
			MatchedReferences: 2,
			MatchedSourceKeys: []string{item.SourceKey, source.SourceKey},
		}},
		Synthesis: topics.TopicSynthesis{
			Overview: "Derived topic synthesis.",
			Angles:   []string{"entity references"},
		},
		Nodes: []topics.TopicMapNode{
			{SourceKey: item.SourceKey, Kind: "item", Title: item.Title, URL: item.CanonicalURL, SourceType: item.SourceType, Role: "seed"},
			{SourceKey: source.SourceKey, Kind: "source", Title: source.Title, URL: source.CanonicalURL, SourceType: source.SourceType, Role: "related"},
		},
		Edges: []topics.TopicMapEdge{{
			From:         item.SourceKey,
			To:           source.SourceKey,
			Relationship: "links_to",
		}},
	}

	bundle, err := BuildBundle(store.OKFExportSnapshot{
		Items:           []model.Item{item},
		Sources:         []model.SourceDocument{source},
		ItemSources:     map[int64][]model.ItemSourceRef{},
		SourceBacklinks: map[int64][]model.SourceBacklink{},
		ItemMedia:       map[int64][]model.ItemMediaRef{},
		ItemChildren:    map[int64][]model.SourceBacklink{},
	}, ExportOptions{
		Profile:         ProfilePrivate,
		IncludeItems:    true,
		IncludeSources:  true,
		IncludeEntities: true,
		IncludeTopics:   true,
		Now:             now,
		Entities:        []entities.Entity{entity},
		Topics:          []topics.TopicMap{graph},
	})
	if err != nil {
		t.Fatalf("BuildBundle: %v", err)
	}
	if bundle.Stats.EntitiesWritten != 1 || bundle.Stats.TopicsWritten != 1 || bundle.Stats.OmittedByFilterLinks != 0 {
		t.Fatalf("unexpected derived stats: %+v", bundle.Stats)
	}
	for _, doc := range bundle.Documents {
		if doc.Type != "Entity" {
			continue
		}
		if len(doc.Sources) != 2 {
			t.Fatalf("entity sources = %+v, want its two evidence references", doc.Sources)
		}
		for _, ref := range doc.Sources {
			if ref.Resource == entity.CanonicalURL || (!strings.Contains(ref.Resource, "items/") && !strings.Contains(ref.Resource, "sources/")) {
				t.Fatalf("entity source is not an exported evidence concept: %+v", ref)
			}
		}
	}
	rendered := renderBundleForTest(t, bundle)
	for _, want := range []string{
		"type: Entity",
		"type: Topic",
		"dbrain_derived: true",
		"# Referenced By",
		"# Key Entities",
		"# Evidence Nodes",
		"Derived topic synthesis.",
		"links_to",
	} {
		if !strings.Contains(rendered, want) {
			t.Fatalf("expected rendered bundle to contain %q\n%s", want, rendered)
		}
	}

	root := t.TempDir()
	if err := writeBundle(root, bundle); err != nil {
		t.Fatalf("writeBundle: %v", err)
	}
	validation, err := ValidateBundle(root)
	if err != nil {
		t.Fatalf("ValidateBundle: %v", err)
	}
	if !validation.Conformant || validation.BrokenInternalLinks != 0 {
		t.Fatalf("unexpected validation result: %+v", validation)
	}
}

func TestBuildBundleDerivedViewsRecordOmittedFilteredLinks(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, 6, 14, 18, 45, 0, 0, time.UTC)
	entity := entities.Entity{
		Key:            "site:missing.example",
		Name:           "Missing Example",
		Kind:           entities.KindSite,
		ReferenceCount: 1,
		References: []entities.Reference{{
			SourceKey:    "src:filtered-out",
			Title:        "Filtered source",
			SourceType:   "web",
			Relationship: "mentions",
		}},
	}
	graph := topics.TopicMap{
		Topic:        "filtered links",
		SeedLimit:    1,
		RelatedLimit: 1,
		Nodes: []topics.TopicMapNode{{
			SourceKey:  "src:filtered-out",
			Kind:       "source",
			Title:      "Filtered source",
			SourceType: "web",
			Role:       "seed",
		}},
	}

	bundle, err := BuildBundle(store.OKFExportSnapshot{}, ExportOptions{
		Profile:         ProfilePrivate,
		IncludeEntities: true,
		IncludeTopics:   true,
		Now:             now,
		Entities:        []entities.Entity{entity},
		Topics:          []topics.TopicMap{graph},
	})
	if err != nil {
		t.Fatalf("BuildBundle: %v", err)
	}
	if bundle.Stats.OmittedByFilterLinks == 0 {
		t.Fatalf("expected omitted-by-filter derived links, got stats %+v", bundle.Stats)
	}
	for _, link := range bundle.Manifest.OmittedLinks {
		if strings.TrimSpace(link.TargetPath) == "" {
			t.Fatalf("omitted link missing target_path: %+v", link)
		}
	}
	root := t.TempDir()
	if err := writeBundle(root, bundle); err != nil {
		t.Fatalf("writeBundle: %v", err)
	}
	validation, err := ValidateBundle(root)
	if err != nil {
		t.Fatalf("ValidateBundle: %v", err)
	}
	if !validation.Conformant || validation.BrokenInternalLinks != 0 || validation.OmittedByFilterLinks == 0 {
		t.Fatalf("unexpected validation result: %+v", validation)
	}
}

func TestAcquireLockBlocksConcurrentAndRecoversStaleFile(t *testing.T) {
	t.Parallel()

	parent := t.TempDir()
	lockPath := filepath.Join(parent, ".dbrain-okf-export.lock")

	unlock, err := acquireLock(parent)
	if err != nil {
		t.Fatalf("acquireLock first: %v", err)
	}
	if _, err := acquireLock(parent); err == nil {
		unlock()
		t.Fatalf("concurrent acquireLock succeeded, want lock contention failure")
	}
	unlock()

	if err := os.WriteFile(lockPath, []byte("stale-pid\n"), 0o644); err != nil {
		t.Fatalf("write stale lock file: %v", err)
	}
	unlock, err = acquireLock(parent)
	if err != nil {
		t.Fatalf("acquireLock with stale lock file: %v", err)
	}
	unlock()
	if _, err := os.Stat(lockPath); err != nil {
		t.Fatalf("lock file should remain as reusable advisory lock file: %v", err)
	}
}

func TestValidateBundleSpecMinimumAndPathSafety(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	writeInspectionManifest(t, root, "2026-07-13T18:00:00Z", []ManifestConcept{{Path: "concept.md", Type: "Thing"}})
	if err := os.WriteFile(filepath.Join(root, "concept.md"), []byte("---\ntype: Thing\n---\n# Thing\n"), 0o644); err != nil {
		t.Fatalf("write concept: %v", err)
	}
	if err := os.WriteFile(filepath.Join(root, "index.md"), []byte("# Index\n"), 0o644); err != nil {
		t.Fatalf("write index: %v", err)
	}
	result, err := ValidateBundle(root)
	if err != nil {
		t.Fatalf("ValidateBundle: %v", err)
	}
	if !result.Conformant || result.Concepts != 1 || result.Indexes != 1 {
		t.Fatalf("unexpected validation result: %+v", result)
	}

	for _, rel := range []string{"../escape.md", "/abs.md", "items/index.md", "items/log.md", "items//bad.md"} {
		if err := ValidateConceptPath(rel); err == nil {
			t.Fatalf("ValidateConceptPath(%q) succeeded, want failure", rel)
		}
	}
}

func TestValidateBundleSkipsMarkedEvidenceLinksOnly(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	writeInspectionManifest(t, root, "2026-07-13T18:00:00Z", []ManifestConcept{{Path: "concept.md", Type: "Thing"}})
	body := "---\ntype: Thing\ntitle: Thing\ndescription: Thing\n---\n" +
		"# Extracted Text\n\n" +
		validationSkipBegin + "\n" +
		"Source payload link: [contributing](CONTRIBUTING.md)\n" +
		validationSkipEnd + "\n\n" +
		"# Related Concepts\n\n" +
		"- [Missing concept](missing.md)\n"
	if err := os.WriteFile(filepath.Join(root, "concept.md"), []byte(body), 0o644); err != nil {
		t.Fatalf("write concept: %v", err)
	}
	result, err := ValidateBundle(root)
	if err != nil {
		t.Fatalf("ValidateBundle: %v", err)
	}
	if !result.Conformant || result.BrokenInternalLinks != 1 {
		t.Fatalf("broken links must remain diagnostic without rejecting the bundle, got %+v", result)
	}
	joined := strings.Join(result.Errors, "\n")
	if strings.Contains(joined, "CONTRIBUTING.md") {
		t.Fatalf("skipped source payload link was validated unexpectedly: %s", joined)
	}
	if strings.Contains(joined, "missing.md") {
		t.Fatalf("broken link was incorrectly promoted to a conformance error: %s", joined)
	}
}

func TestExportWritesPrivateBundleAtomically(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	root := t.TempDir()
	cfg := config.Config{
		DBPath:   filepath.Join(root, "brain.db"),
		OKFDir:   filepath.Join(root, "okf", "current"),
		VaultDir: filepath.Join(root, "vault"),
	}
	st, err := store.Open(cfg.DBPath)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer func() { _ = st.Close() }()

	now := time.Date(2026, 6, 14, 18, 0, 0, 0, time.UTC)
	itemResult, err := st.UpsertItem(ctx, model.Item{
		SourceKey:    "x:okf-export",
		SourceType:   "x_bookmark",
		ExternalID:   "204",
		CanonicalURL: "https://x.com/example/status/204",
		Title:        "OKF export item",
		Text:         "raw item",
		SummaryText:  "item summary",
		ContentHash:  "item-hash",
		LinksJSON:    `["https://example.com/page"]`,
		NotePath:     "items/x/2026/204.md",
		UpdatedAt:    now,
		LastSeenAt:   now,
	})
	if err != nil {
		t.Fatalf("UpsertItem: %v", err)
	}
	link, err := st.UpsertSourceLink(ctx, itemResult.ItemID, model.SourceCandidate{
		OriginalURL:   "https://example.com/page",
		CanonicalURL:  "https://example.com/page",
		NormalizedURL: "https://example.com/page",
		SourceType:    "web",
		Domain:        "example.com",
		SourceKey:     "src:okf-export",
		NotePath:      "sources/web/page.md",
	})
	if err != nil {
		t.Fatalf("UpsertSourceLink: %v", err)
	}
	if _, err := st.SaveSourceSummary(ctx, link.SourceID, model.SummaryResult{
		Text:          "source summary",
		Status:        model.SourceSummaryStatusOK,
		Model:         "test/model",
		PromptVersion: "source-summary-v1",
		FetchedAt:     now,
	}); err != nil {
		t.Fatalf("SaveSourceSummary: %v", err)
	}

	result, err := Export(ctx, cfg, st, ExportOptions{Profile: ProfilePrivate, IncludeItems: true, IncludeSources: true, Now: now})
	if err != nil {
		t.Fatalf("Export: %v", err)
	}
	if result.Bundle != cfg.OKFDir || result.ItemsWritten != 1 || result.SourcesWritten != 1 || result.BrokenInternalLinks != 0 {
		t.Fatalf("unexpected export result: %+v", result)
	}
	if _, err := os.Stat(filepath.Join(cfg.OKFDir, manifestFileName)); err != nil {
		t.Fatalf("manifest missing: %v", err)
	}
	validation, err := ValidateBundle(cfg.OKFDir)
	if err != nil {
		t.Fatalf("ValidateBundle exported: %v", err)
	}
	if !validation.Conformant {
		t.Fatalf("exported bundle not conformant: %+v", validation)
	}

	if _, err := Export(ctx, cfg, st, ExportOptions{Profile: "public"}); err == nil {
		t.Fatalf("non-private profile export succeeded, want MVP not implemented error")
	}
}

func renderBundleForTest(t *testing.T, bundle Bundle) string {
	t.Helper()
	var b strings.Builder
	for _, doc := range bundle.Documents {
		b.WriteString("=== ")
		b.WriteString(doc.Path)
		b.WriteString("\n")
		if filepath.Base(doc.Path) == "index.md" {
			b.WriteString(doc.Body)
			continue
		}
		rendered, err := RenderDocument(doc)
		if err != nil {
			t.Fatalf("RenderDocument(%s): %v", doc.Path, err)
		}
		b.WriteString(rendered)
	}
	return b.String()
}
