package audit

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"
)

type fakeUpstreamInventory struct {
	result    InventoryResult
	err       error
	calls     int
	remaining time.Duration
	active    *upstreamConcurrency
}

type contextIgnoringErrorInventory struct{}

func (contextIgnoringErrorInventory) Inventory(ctx context.Context, _ InventoryBudget) (InventoryResult, error) {
	<-ctx.Done()
	return InventoryResult{}, errors.New("adapter returned an unwrapped error after cancellation")
}

type contextIgnoringInvalidInventory struct{}

func (contextIgnoringInvalidInventory) Inventory(ctx context.Context, _ InventoryBudget) (InventoryResult, error) {
	<-ctx.Done()
	return InventoryResult{IdentityHashes: []string{"not-a-hash"}, PageCount: -1, Complete: true}, errors.New("adapter returned invalid data after cancellation")
}

type cancelingUpstreamInventory struct{ cancel context.CancelFunc }

func (i cancelingUpstreamInventory) Inventory(context.Context, InventoryBudget) (InventoryResult, error) {
	i.cancel()
	return InventoryResult{}, context.Canceled
}

func (f *fakeUpstreamInventory) Inventory(ctx context.Context, budget InventoryBudget) (InventoryResult, error) {
	f.calls++
	if budget != DefaultInventoryBudget() {
		return InventoryResult{}, errors.New("unexpected inventory budget")
	}
	if deadline, ok := ctx.Deadline(); ok {
		f.remaining = time.Until(deadline)
	}
	if f.active != nil {
		f.active.enter()
		defer f.active.leave()
		time.Sleep(2 * time.Millisecond)
	}
	return f.result, f.err
}

type upstreamConcurrency struct {
	mu        sync.Mutex
	active    int
	maxActive int
}

func (c *upstreamConcurrency) enter() {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.active++
	if c.active > c.maxActive {
		c.maxActive = c.active
	}
}

func (c *upstreamConcurrency) leave() {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.active--
}

type upstreamMatchStore struct {
	fakeStore
	matched int
	err     error
	calls   int
	sources []Source
	hashes  [][]string
}

func (s *upstreamMatchStore) CountLocalIdentityMatches(_ context.Context, source Source, hashes []string) (int, error) {
	s.calls++
	s.sources = append(s.sources, source)
	s.hashes = append(s.hashes, append([]string(nil), hashes...))
	return s.matched, s.err
}

func TestHashUpstreamIdentityIsClosedDomainSeparatedAndStable(t *testing.T) {
	xHash, err := HashUpstreamIdentity(SourceXBookmarks, "x:123")
	if err != nil {
		t.Fatal(err)
	}
	githubHash, err := HashUpstreamIdentity(SourceGitHubStars, "x:123")
	if err != nil {
		t.Fatal(err)
	}
	second, err := HashUpstreamIdentity(SourceXBookmarks, "x:123")
	if err != nil {
		t.Fatal(err)
	}
	if len(xHash) != 64 || xHash != strings.ToLower(xHash) || xHash == githubHash || xHash != second {
		t.Fatalf("hashes are not stable and domain separated: x=%q github=%q second=%q", xHash, githubHash, second)
	}
	if want := "61870beb388d1e3a983d1d78909980b7f4f5ab93d11f761c7aed5831bcbb345a"; xHash != want {
		t.Fatalf("x identity hash = %q, want locked v1 vector %q", xHash, want)
	}
	if _, err := HashUpstreamIdentity(Source("future-source"), "value"); err == nil {
		t.Fatal("expected unknown source to fail closed")
	}
	if _, err := HashUpstreamIdentity(SourceXBookmarks, "  "); err == nil {
		t.Fatal("expected empty identity to fail closed")
	}
	feedHash, err := HashUpstreamFeedIdentity("feed:key", "guid:item")
	if err != nil {
		t.Fatal(err)
	}
	directFeedHash, err := HashUpstreamIdentity(SourceFeeds, "feed:key\x00guid:item")
	if err != nil {
		t.Fatal(err)
	}
	if feedHash != directFeedHash {
		t.Fatalf("feed hash = %q, want %q", feedHash, directFeedHash)
	}
	if want := "72a09cc7a332aa2af975f7f82ccc387c23e765a9ec5f69139352ff5062d9cab6"; feedHash != want {
		t.Fatalf("feed identity hash = %q, want locked v1 vector %q", feedHash, want)
	}
}

func TestNormalizeInventoryResultDeduplicatesAndRejectsInvalidOrOverBudgetResults(t *testing.T) {
	one, _ := HashUpstreamIdentity(SourceXBookmarks, "x:1")
	two, _ := HashUpstreamIdentity(SourceXBookmarks, "x:2")
	got, err := normalizeInventoryResult(InventoryResult{IdentityHashes: []string{two, one, one}, PageCount: 2, Complete: true}, DefaultInventoryBudget())
	if err != nil {
		t.Fatal(err)
	}
	want := []string{one, two}
	sort.Strings(want)
	if !slices.Equal(got.IdentityHashes, want) || !got.Complete {
		t.Fatalf("normalized = %#v", got)
	}
	empty, err := normalizeInventoryResult(InventoryResult{PageCount: 0, Complete: true}, DefaultInventoryBudget())
	if err != nil || !empty.Complete || empty.PageCount != 0 {
		t.Fatalf("complete zero-page empty inventory = %#v err=%v", empty, err)
	}
	for _, value := range []InventoryResult{
		{IdentityHashes: []string{strings.ToUpper(one)}, PageCount: 1, Complete: true},
		{IdentityHashes: []string{one}, PageCount: InventoryMaxPages + 1, Complete: true},
	} {
		if _, err := normalizeInventoryResult(value, DefaultInventoryBudget()); err == nil {
			t.Fatalf("expected invalid result to fail: %#v", value)
		}
	}
	rawDuplicates := make([]string, InventoryMaxIdentities+1)
	for i := range rawDuplicates {
		rawDuplicates[i] = one
	}
	deduplicated, err := normalizeInventoryResult(InventoryResult{IdentityHashes: rawDuplicates, PageCount: 1, Complete: true}, DefaultInventoryBudget())
	if err != nil || len(deduplicated.IdentityHashes) != 1 {
		t.Fatalf("raw duplicates exceeded unique cap: count=%d err=%v", len(deduplicated.IdentityHashes), err)
	}
	if _, err := normalizeInventoryResult(InventoryResult{PageCount: -1}, DefaultInventoryBudget()); !errors.Is(err, ErrInventoryInvalid) {
		t.Fatalf("negative page count error = %v, want ErrInventoryInvalid", err)
	}
	overPages, err := normalizeInventoryResult(InventoryResult{PageCount: InventoryMaxPages + 1}, DefaultInventoryBudget())
	if !errors.Is(err, ErrInventoryBudget) {
		t.Fatalf("over-cap page count error = %v, want ErrInventoryBudget", err)
	}
	if overPages.PageCount != InventoryMaxPages {
		t.Fatalf("over-cap page count evidence = %d, want bounded %d", overPages.PageCount, InventoryMaxPages)
	}
}

func TestNormalizeInventoryResultAcceptsExactCapsOnlyWhenAdapterObservedCompletion(t *testing.T) {
	hashes := make([]string, InventoryMaxIdentities)
	for i := range hashes {
		hashes[i], _ = HashUpstreamIdentity(SourceXBookmarks, fmt.Sprintf("x:%d", i))
	}
	complete, err := normalizeInventoryResult(InventoryResult{IdentityHashes: hashes, PageCount: InventoryMaxPages, Complete: true}, DefaultInventoryBudget())
	if err != nil || !complete.Complete || len(complete.IdentityHashes) != InventoryMaxIdentities {
		t.Fatalf("exact complete cap rejected: count=%d complete=%t err=%v", len(complete.IdentityHashes), complete.Complete, err)
	}
	incomplete, err := normalizeInventoryResult(InventoryResult{IdentityHashes: hashes, PageCount: InventoryMaxPages}, DefaultInventoryBudget())
	if err != nil || incomplete.Complete {
		t.Fatalf("core invented completion at exact cap: complete=%t err=%v", incomplete.Complete, err)
	}
	overUnique := append(append([]string(nil), hashes...), "")
	overUnique[len(overUnique)-1], _ = HashUpstreamIdentity(SourceXBookmarks, "x:over-cap")
	if _, err := normalizeInventoryResult(InventoryResult{IdentityHashes: overUnique, PageCount: InventoryMaxPages, Complete: true}, DefaultInventoryBudget()); !errors.Is(err, ErrInventoryBudget) {
		t.Fatalf("over unique identity cap error = %v, want ErrInventoryBudget", err)
	}
}

func TestRunDeepRejectsSourceOverrideOutsideDeclaredParityScope(t *testing.T) {
	deps := passingDependencies(time.Date(2026, 7, 14, 12, 0, 0, 0, time.UTC))
	for name, req := range map[string]Request{
		"missing source filter": {Profile: ProfileDeep, Categories: []Category{CategoryImports}, SourceOverrides: []Source{SourceGitHubStars}},
		"wrong category":        {Profile: ProfileDeep, Categories: []Category{CategoryPipeline}, Sources: []Source{SourceGitHubStars}, SourceOverrides: []Source{SourceGitHubStars}},
		"wrong check":           {Profile: ProfileDeep, Sources: []Source{SourceGitHubStars}, SourceOverrides: []Source{SourceGitHubStars}, CheckIDs: []CheckID{CheckImportsGitHubStarsPoll}},
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := RunDeep(t.Context(), req, deps, DeepDependencies{Limits: DefaultDeepLimits()}); err == nil {
				t.Fatal("expected out-of-scope source override to be rejected")
			}
		})
	}
}

func TestRunDeepRejectsImpossibleLocalMatchCount(t *testing.T) {
	now := time.Date(2026, 7, 14, 12, 0, 0, 0, time.UTC)
	identity, _ := HashUpstreamIdentity(SourceGitHubStars, "gh-star:viewer:owner/repo")
	deps := passingDependencies(now)
	store := &upstreamMatchStore{fakeStore: deps.Store.(fakeStore), matched: 2}
	deps.Store = store
	report, err := RunDeep(t.Context(), Request{Profile: ProfileDeep, Categories: []Category{CategoryImports}, Sources: []Source{SourceGitHubStars}}, deps, DeepDependencies{
		Upstream: UpstreamInventories{SourceGitHubStars: &fakeUpstreamInventory{result: InventoryResult{IdentityHashes: []string{identity}, PageCount: 1, Complete: true}}}, Limits: DefaultDeepLimits(),
	})
	if err != nil {
		t.Fatal(err)
	}
	check := checkByIDForUpstreamTest(t, report, CheckUpstreamGitHubStarsParity)
	if check.Status != StatusUnknown || check.ErrorCode != ErrorDatabase || check.Evidence["inventory_complete"] != false {
		t.Fatalf("impossible matcher result = %#v", check)
	}
}

func TestRunDeepUpstreamTimeoutWinsOverUnwrappedAdapterError(t *testing.T) {
	now := time.Date(2026, 7, 14, 12, 0, 0, 0, time.UTC)
	deps := passingDependencies(now)
	deps.Features.Timeouts = map[TimeoutClass]time.Duration{TimeoutUpstreamInventory: 5 * time.Millisecond}
	deps.Store = &upstreamMatchStore{fakeStore: deps.Store.(fakeStore)}
	report, err := RunDeep(t.Context(), Request{Profile: ProfileDeep, Categories: []Category{CategoryImports}, Sources: []Source{SourceGitHubStars}}, deps, DeepDependencies{
		Upstream: UpstreamInventories{SourceGitHubStars: contextIgnoringErrorInventory{}}, Limits: DefaultDeepLimits(),
	})
	if err != nil {
		t.Fatal(err)
	}
	check := checkByIDForUpstreamTest(t, report, CheckUpstreamGitHubStarsParity)
	if check.Status != StatusUnknown || check.ErrorCode != ErrorTimeout {
		t.Fatalf("timed out adapter = %#v", check)
	}
}

func TestRunDeepUpstreamTimeoutWinsOverInvalidReturnedResult(t *testing.T) {
	now := time.Date(2026, 7, 14, 12, 0, 0, 0, time.UTC)
	deps := passingDependencies(now)
	deps.Features.Timeouts = map[TimeoutClass]time.Duration{TimeoutUpstreamInventory: 5 * time.Millisecond}
	deps.Store = &upstreamMatchStore{fakeStore: deps.Store.(fakeStore)}
	report, err := RunDeep(t.Context(), Request{Profile: ProfileDeep, Categories: []Category{CategoryImports}, Sources: []Source{SourceGitHubStars}}, deps, DeepDependencies{
		Upstream: UpstreamInventories{SourceGitHubStars: contextIgnoringInvalidInventory{}}, Limits: DefaultDeepLimits(),
	})
	if err != nil {
		t.Fatal(err)
	}
	check := checkByIDForUpstreamTest(t, report, CheckUpstreamGitHubStarsParity)
	if check.Status != StatusUnknown || check.ErrorCode != ErrorTimeout || check.Evidence["upstream_count"] != 0 || check.Evidence["page_count"] != 0 {
		t.Fatalf("timed out invalid adapter = %#v", check)
	}
}

func TestRunDeepParentCancellationStopsLaterInventoriesButSourceTimeoutDoesNot(t *testing.T) {
	now := time.Date(2026, 7, 14, 12, 0, 0, 0, time.UTC)
	features := allFeatures()
	for source := range features.Sources {
		features.Sources[source] = source == SourceGitHubStars || source == SourceFeeds
	}
	deps := passingDependencies(now)
	deps.Features = features
	deps.Store = &upstreamMatchStore{fakeStore: deps.Store.(fakeStore)}

	parentCtx, parentCancel := context.WithCancel(context.Background())
	feedsAfterCancel := &fakeUpstreamInventory{result: InventoryResult{Complete: true}}
	_, _ = RunDeep(parentCtx, Request{Profile: ProfileDeep, Categories: []Category{CategoryImports}}, deps, DeepDependencies{
		Upstream: UpstreamInventories{SourceGitHubStars: cancelingUpstreamInventory{cancel: parentCancel}, SourceFeeds: feedsAfterCancel}, Limits: DefaultDeepLimits(),
	})
	if feedsAfterCancel.calls != 0 {
		t.Fatalf("later inventory called %d times after parent cancellation", feedsAfterCancel.calls)
	}

	deps.Features.Timeouts = map[TimeoutClass]time.Duration{TimeoutUpstreamInventory: 5 * time.Millisecond}
	feedsAfterTimeout := &fakeUpstreamInventory{result: InventoryResult{Complete: true}}
	report, err := RunDeep(t.Context(), Request{Profile: ProfileDeep, Categories: []Category{CategoryImports}}, deps, DeepDependencies{
		Upstream: UpstreamInventories{SourceGitHubStars: contextIgnoringErrorInventory{}, SourceFeeds: feedsAfterTimeout}, Limits: DefaultDeepLimits(),
	})
	if err != nil {
		t.Fatal(err)
	}
	if feedsAfterTimeout.calls != 1 || checkByIDForUpstreamTest(t, report, CheckUpstreamGitHubStarsParity).ErrorCode != ErrorTimeout {
		t.Fatalf("per-source timeout stopped later inventory: feeds=%d checks=%#v", feedsAfterTimeout.calls, report.Checks)
	}
}

func TestRunDeepUpstreamParityClassifiesCountsWithoutLeakingIdentityMaterial(t *testing.T) {
	now := time.Date(2026, 7, 14, 12, 0, 0, 0, time.UTC)
	one, _ := HashUpstreamIdentity(SourceGitHubStars, "gh-star:viewer:owner/private-name")
	two, _ := HashUpstreamIdentity(SourceGitHubStars, "gh-star:viewer:owner/other-private-name")
	tests := []struct {
		name         string
		result       InventoryResult
		inventoryErr error
		matched      int
		matchErr     error
		wantStatus   Status
		wantCode     ErrorCode
		wantMissing  int
		wantCalls    int
	}{
		{name: "complete match", result: InventoryResult{IdentityHashes: []string{one, two}, PageCount: 2, Complete: true}, matched: 2, wantStatus: StatusPass, wantCalls: 1},
		{name: "complete omission", result: InventoryResult{IdentityHashes: []string{one, two}, PageCount: 2, Complete: true}, matched: 1, wantStatus: StatusFail, wantMissing: 1, wantCalls: 1},
		{name: "incomplete", result: InventoryResult{IdentityHashes: []string{one}, PageCount: 1}, wantStatus: StatusUnknown, wantCode: ErrorListingIncomplete},
		{name: "inventory error", result: InventoryResult{IdentityHashes: []string{one}, PageCount: 1}, inventoryErr: errors.New("secret upstream failure owner/private-name"), wantStatus: StatusUnknown, wantCode: ErrorRead},
		{name: "local match error", result: InventoryResult{IdentityHashes: []string{one}, PageCount: 1, Complete: true}, matchErr: errors.New("secret database failure owner/private-name"), wantStatus: StatusUnknown, wantCode: ErrorDatabase, wantCalls: 1},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			deps := passingDependencies(now)
			store := &upstreamMatchStore{fakeStore: deps.Store.(fakeStore), matched: test.matched, err: test.matchErr}
			deps.Store = store
			inventory := &fakeUpstreamInventory{result: test.result, err: test.inventoryErr}
			report, err := RunDeep(t.Context(), Request{Profile: ProfileDeep, Categories: []Category{CategoryImports}, Sources: []Source{SourceGitHubStars}}, deps, DeepDependencies{
				Upstream: UpstreamInventories{SourceGitHubStars: inventory}, Limits: DefaultDeepLimits(),
			})
			if err != nil {
				t.Fatal(err)
			}
			check := checkByIDForUpstreamTest(t, report, CheckUpstreamGitHubStarsParity)
			if check.Status != test.wantStatus || check.ErrorCode != test.wantCode || !check.Required {
				t.Fatalf("check = %#v", check)
			}
			if got := check.Evidence["missing_local_count"]; got != test.wantMissing {
				t.Fatalf("missing_local_count = %#v, want %d", got, test.wantMissing)
			}
			if store.calls != test.wantCalls {
				t.Fatalf("local matcher calls = %d, want %d", store.calls, test.wantCalls)
			}
			encoded, err := json.Marshal(report)
			if err != nil {
				t.Fatal(err)
			}
			for _, secret := range []string{one, two, "owner/private-name", "secret upstream failure", "secret database failure"} {
				if strings.Contains(string(encoded), secret) {
					t.Fatalf("portable report leaked %q: %s", secret, encoded)
				}
			}
		})
	}
}

func TestRunDeepUpstreamInventoriesUseFiveMinuteContextsSequentially(t *testing.T) {
	now := time.Date(2026, 7, 14, 12, 0, 0, 0, time.UTC)
	deps := passingDependencies(now)
	store := &upstreamMatchStore{fakeStore: deps.Store.(fakeStore)}
	deps.Store = store
	concurrency := &upstreamConcurrency{}
	github := &fakeUpstreamInventory{result: InventoryResult{PageCount: 1, Complete: true}, active: concurrency}
	feeds := &fakeUpstreamInventory{result: InventoryResult{PageCount: 1, Complete: true}, active: concurrency}
	report, err := RunDeep(t.Context(), Request{Profile: ProfileDeep, Categories: []Category{CategoryImports}}, deps, DeepDependencies{
		Upstream: UpstreamInventories{SourceGitHubStars: github, SourceFeeds: feeds}, Limits: DefaultDeepLimits(),
	})
	if err != nil {
		t.Fatal(err)
	}
	if checkByIDForUpstreamTest(t, report, CheckUpstreamGitHubStarsParity).Status != StatusPass || checkByIDForUpstreamTest(t, report, CheckUpstreamFeedsParity).Status != StatusPass {
		t.Fatalf("unexpected parity checks: %#v", report.Checks)
	}
	if concurrency.maxActive != 1 {
		t.Fatalf("maximum upstream concurrency = %d, want 1", concurrency.maxActive)
	}
	for name, remaining := range map[string]time.Duration{"github": github.remaining, "feeds": feeds.remaining} {
		if remaining < 4*time.Minute+59*time.Second || remaining > 5*time.Minute {
			t.Fatalf("%s inventory deadline remaining = %s, want five-minute ceiling", name, remaining)
		}
	}
}

func TestDeepParityConfiguredSetAndExplicitDisabledOverride(t *testing.T) {
	now := time.Date(2026, 7, 14, 12, 0, 0, 0, time.UTC)
	configured := allFeatures()
	for source := range configured.Sources {
		configured.Sources[source] = false
	}
	configured.Sources[SourceGitHubStars] = true
	deps := passingDependencies(now)
	deps.Features = configured
	store := &upstreamMatchStore{fakeStore: deps.Store.(fakeStore)}
	deps.Store = store
	github := &fakeUpstreamInventory{result: InventoryResult{PageCount: 1, Complete: true}}
	feeds := &fakeUpstreamInventory{result: InventoryResult{PageCount: 1, Complete: true}}
	report, err := RunDeep(t.Context(), Request{Profile: ProfileDeep, Categories: []Category{CategoryImports}}, deps, DeepDependencies{
		Upstream: UpstreamInventories{SourceGitHubStars: github, SourceFeeds: feeds}, Limits: DefaultDeepLimits(),
	})
	if err != nil {
		t.Fatal(err)
	}
	if github.calls != 1 || feeds.calls != 0 {
		t.Fatalf("configured inventory calls github=%d feeds=%d", github.calls, feeds.calls)
	}
	if checkByIDForUpstreamTest(t, report, CheckUpstreamFeedsParity).SkipReason != SkipFeatureDisabled {
		t.Fatalf("disabled feed parity = %#v", checkByIDForUpstreamTest(t, report, CheckUpstreamFeedsParity))
	}

	overrideFeatures := configured
	overrideFeatures.SchedulerEnabled = true
	overrideFeatures.Sources[SourceGitHubStars] = false
	deps.Features = overrideFeatures
	github.calls = 0
	report, err = RunDeep(t.Context(), Request{
		Profile: ProfileDeep, Categories: []Category{CategoryImports}, Sources: []Source{SourceGitHubStars}, SourceOverrides: []Source{SourceGitHubStars},
	}, deps, DeepDependencies{Upstream: UpstreamInventories{SourceGitHubStars: github}, Limits: DefaultDeepLimits()})
	if err != nil {
		t.Fatal(err)
	}
	if github.calls != 1 {
		t.Fatalf("explicit disabled override inventory calls = %d, want 1", github.calls)
	}
	poll := checkByIDForUpstreamTest(t, report, CheckImportsGitHubStarsPoll)
	parity := checkByIDForUpstreamTest(t, report, CheckUpstreamGitHubStarsParity)
	if poll.Status != StatusSkipped || poll.SkipReason != SkipFeatureDisabled || parity.Status != StatusPass || !parity.Required {
		t.Fatalf("poll=%#v parity=%#v", poll, parity)
	}
}

func TestAllSevenParityChecksHaveClosedExecutorsAndRequiredSourceCondition(t *testing.T) {
	want := map[CheckID]Source{
		CheckUpstreamAppleNotesParity: SourceAppleNotes, CheckUpstreamSafariTabsParity: SourceSafariTabs,
		CheckUpstreamXBookmarksParity: SourceXBookmarks, CheckUpstreamGitHubStarsParity: SourceGitHubStars,
		CheckUpstreamYouTubeLikedParity: SourceYouTubeLiked, CheckUpstreamYouTubeWatchLaterParity: SourceYouTubeWatchLater,
		CheckUpstreamFeedsParity: SourceFeeds,
	}
	if len(upstreamCheckSources) != len(want) {
		t.Fatalf("upstream executor source map has %d entries, want %d", len(upstreamCheckSources), len(want))
	}
	for id, source := range want {
		entry, ok := Lookup(id)
		if !ok || entry.Source != source || entry.RequiredWhen != RequiredSource || !HasExecutor(id) || upstreamCheckSources[id] != source {
			t.Fatalf("parity %s entry=%#v executor=%t mapped=%s", id, entry, HasExecutor(id), upstreamCheckSources[id])
		}
	}
}

func TestStandardProfileExcludesAllParityChecks(t *testing.T) {
	report, err := Run(t.Context(), Request{Profile: ProfileStandard, Categories: []Category{CategoryImports}}, passingDependencies(time.Date(2026, 7, 14, 12, 0, 0, 0, time.UTC)))
	if err != nil {
		t.Fatal(err)
	}
	for id := range upstreamCheckSources {
		check := checkByIDForUpstreamTest(t, report, id)
		if check.Status != StatusSkipped || check.SkipReason != SkipProfileExcluded || check.Required {
			t.Fatalf("standard parity %s = %#v", id, check)
		}
	}
}

func checkByIDForUpstreamTest(t *testing.T, report Report, id CheckID) Check {
	t.Helper()
	for _, check := range report.Checks {
		if check.ID == id {
			return check
		}
	}
	t.Fatalf("check %s not found", id)
	return Check{}
}
