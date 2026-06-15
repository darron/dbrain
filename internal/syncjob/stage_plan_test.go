package syncjob

import "testing"

func TestDefaultSyncStagePlanOrder(t *testing.T) {
	plan := defaultSyncStagePlan()
	if err := validateSyncStagePlan(plan); err != nil {
		t.Fatalf("validateSyncStagePlan: %v", err)
	}

	got := make([]syncStageID, 0, len(plan))
	for _, stage := range plan {
		got = append(got, stage.ID)
	}
	want := []syncStageID{
		syncStageAppleNotes,
		syncStageSafariTabs,
		syncStageXFrontier,
		syncStageXMedia,
		syncStageXPhotoOCR,
		syncStageGitHub,
		syncStageYouTube,
		syncStageFeeds,
		syncStageSources,
		syncStageCategorize,
		syncStageMediaArchive,
		syncStageOKFExport,
	}
	if len(got) != len(want) {
		t.Fatalf("stage count mismatch: got %d want %d (%v)", len(got), len(want), got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("stage %d mismatch: got %q want %q (all=%v)", i, got[i], want[i], got)
		}
	}
}

func TestDefaultSyncStagePlanEnabledPredicates(t *testing.T) {
	plan := defaultSyncStagePlan()
	byID := map[syncStageID]syncStage{}
	for _, stage := range plan {
		byID[stage.ID] = stage
	}

	opts := stageOptions{}
	if byID[syncStageXFrontier].Enabled(opts) {
		t.Fatal("x frontier should be disabled when x bookmarks, hydration, and links are disabled")
	}

	opts.XBookmarks.Enabled = true
	if !byID[syncStageXFrontier].Enabled(opts) {
		t.Fatal("x frontier should be enabled by x bookmarks")
	}
	opts.XBookmarks.Enabled = false
	opts.X.Enabled = true
	if !byID[syncStageXFrontier].Enabled(opts) {
		t.Fatal("x frontier should be enabled by x hydration")
	}
	opts.X.Enabled = false
	opts.Links.Enabled = true
	if !byID[syncStageXFrontier].Enabled(opts) {
		t.Fatal("x frontier should be enabled by link extraction")
	}

	stageChecks := []struct {
		id     syncStageID
		enable func(*stageOptions)
	}{
		{syncStageAppleNotes, func(opts *stageOptions) { opts.AppleNotes.Enabled = true }},
		{syncStageSafariTabs, func(opts *stageOptions) { opts.SafariTabs.Enabled = true }},
		{syncStageXMedia, func(opts *stageOptions) { opts.XMedia.Enabled = true }},
		{syncStageXPhotoOCR, func(opts *stageOptions) { opts.XPhotoOCR.Enabled = true }},
		{syncStageGitHub, func(opts *stageOptions) { opts.GitHub.Enabled = true }},
		{syncStageYouTube, func(opts *stageOptions) { opts.YouTube.Enabled = true }},
		{syncStageFeeds, func(opts *stageOptions) { opts.Feeds.Enabled = true }},
		{syncStageSources, func(opts *stageOptions) { opts.Sources.Enabled = true }},
		{syncStageCategorize, func(opts *stageOptions) { opts.Categorize.Enabled = true }},
		{syncStageMediaArchive, func(opts *stageOptions) { opts.Archive.Enabled = true }},
		{syncStageOKFExport, func(opts *stageOptions) { opts.OKFExport.Enabled = true }},
	}
	for _, check := range stageChecks {
		opts := stageOptions{}
		if byID[check.id].Enabled(opts) {
			t.Fatalf("%s should be disabled by default", check.id)
		}
		check.enable(&opts)
		if !byID[check.id].Enabled(opts) {
			t.Fatalf("%s should be enabled by its stage option", check.id)
		}
	}
}
