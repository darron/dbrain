package audit

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"regexp"
	"sort"
	"strings"
)

const (
	InventoryMaxIdentities = 100_000
	InventoryMaxPages      = 10_000
	maxRawIdentityBytes    = 64 << 10
	identityHashDomainV1   = "dbrain.audit.identity.v1"
)

var (
	ErrInventoryInvalid    = errors.New("upstream inventory result invalid")
	ErrInventoryIncomplete = errors.New("upstream inventory traversal incomplete")
	ErrInventoryBudget     = errors.New("upstream inventory budget exhausted")
	identityHashPattern    = regexp.MustCompile(`^[0-9a-f]{64}$`)
)

// InventoryBudget is a fixed, caller-owned upper bound. Importer adapters may
// stop earlier, but cannot widen these ceilings.
type InventoryBudget struct {
	MaxIdentities int
	MaxPages      int
}

func DefaultInventoryBudget() InventoryBudget {
	return InventoryBudget{MaxIdentities: InventoryMaxIdentities, MaxPages: InventoryMaxPages}
}

// InventoryResult intentionally contains no raw identities or source content.
// Complete is asserted only by the importer after observing its natural end.
type InventoryResult struct {
	IdentityHashes []string
	PageCount      int
	Complete       bool
}

type UpstreamInventory interface {
	Inventory(context.Context, InventoryBudget) (InventoryResult, error)
}

// UpstreamInventories is keyed by the closed Source enum. Validation rejects
// future or misspelled keys rather than silently granting them authority.
type UpstreamInventories map[Source]UpstreamInventory

func (i UpstreamInventories) validate() error {
	for source := range i {
		if !source.Valid() {
			return fmt.Errorf("invalid upstream inventory source %q", source)
		}
	}
	return nil
}

// HashUpstreamIdentity produces a content-free, source-domain-separated value
// suitable for local set membership. The raw identity is never report evidence.
func HashUpstreamIdentity(source Source, identity string) (string, error) {
	if !source.Valid() {
		return "", fmt.Errorf("invalid upstream identity source %q", source)
	}
	identity = strings.TrimSpace(identity)
	if identity == "" || len(identity) > maxRawIdentityBytes {
		return "", fmt.Errorf("invalid upstream identity length")
	}
	digest := sha256.Sum256([]byte(identityHashDomainV1 + "\x00" + string(source) + "\x00" + identity))
	return hex.EncodeToString(digest[:]), nil
}

// HashUpstreamFeedIdentity preserves feed identity evolution by binding an
// entry identity (guid:, link:, or stored identity_key) to its feed key.
func HashUpstreamFeedIdentity(feedKey, identity string) (string, error) {
	feedKey = strings.TrimSpace(feedKey)
	identity = strings.TrimSpace(identity)
	if feedKey == "" || identity == "" || strings.ContainsRune(feedKey, '\x00') || strings.ContainsRune(identity, '\x00') {
		return "", fmt.Errorf("invalid feed identity")
	}
	return HashUpstreamIdentity(SourceFeeds, feedKey+"\x00"+identity)
}

func normalizeInventoryResult(value InventoryResult, budget InventoryBudget) (InventoryResult, error) {
	if budget.MaxIdentities <= 0 || budget.MaxIdentities > InventoryMaxIdentities || budget.MaxPages <= 0 || budget.MaxPages > InventoryMaxPages {
		return InventoryResult{}, fmt.Errorf("%w: invalid budget", ErrInventoryInvalid)
	}
	if value.PageCount < 0 || value.PageCount > budget.MaxPages {
		return InventoryResult{PageCount: max(0, min(value.PageCount, budget.MaxPages))}, fmt.Errorf("%w: page count", ErrInventoryBudget)
	}
	if len(value.IdentityHashes) > budget.MaxIdentities {
		return InventoryResult{PageCount: value.PageCount}, fmt.Errorf("%w: identity count", ErrInventoryBudget)
	}
	seen := make(map[string]struct{}, len(value.IdentityHashes))
	hashes := make([]string, 0, len(value.IdentityHashes))
	for _, hash := range value.IdentityHashes {
		if !identityHashPattern.MatchString(hash) {
			return InventoryResult{PageCount: value.PageCount}, fmt.Errorf("%w: identity hash", ErrInventoryInvalid)
		}
		if _, ok := seen[hash]; ok {
			continue
		}
		seen[hash] = struct{}{}
		hashes = append(hashes, hash)
	}
	sort.Strings(hashes)
	return InventoryResult{IdentityHashes: hashes, PageCount: value.PageCount, Complete: value.Complete}, nil
}

var upstreamCheckSources = map[CheckID]Source{
	CheckUpstreamAppleNotesParity:        SourceAppleNotes,
	CheckUpstreamSafariTabsParity:        SourceSafariTabs,
	CheckUpstreamXBookmarksParity:        SourceXBookmarks,
	CheckUpstreamGitHubStarsParity:       SourceGitHubStars,
	CheckUpstreamYouTubeLikedParity:      SourceYouTubeLiked,
	CheckUpstreamYouTubeWatchLaterParity: SourceYouTubeWatchLater,
	CheckUpstreamFeedsParity:             SourceFeeds,
}

type upstreamObservation struct {
	result    InventoryResult
	matched   int
	err       error
	errorCode ErrorCode
}

func executeUpstream(_ context.Context, s *runState, entry RegistryEntry) Check {
	observation, ok := s.upstream[entry.Source]
	if !ok {
		observation = upstreamObservation{err: errCapabilityUnavailable, errorCode: ErrorUnavailable}
	}
	upstreamCount := len(observation.result.IdentityHashes)
	matched := observation.matched
	missing := 0
	complete := observation.err == nil && observation.result.Complete
	if complete {
		missing = upstreamCount - matched
	}
	evidence := Evidence{
		"upstream_count":      upstreamCount,
		"matched_local_count": matched,
		"missing_local_count": missing,
		"page_count":          observation.result.PageCount,
		"inventory_complete":  complete,
	}
	if observation.err != nil || !complete {
		code := observation.errorCode
		if code == "" {
			code = ErrorListingIncomplete
		}
		check := baseCheck(entry, s.now, StatusUnknown, ConfidenceUnknown, evidence)
		check.ErrorCode = code
		return check
	}
	if missing > 0 {
		return baseCheck(entry, s.now, StatusFail, ConfidenceHigh, evidence)
	}
	return baseCheck(entry, s.now, StatusPass, ConfidenceHigh, evidence)
}

func upstreamErrorCode(err error) ErrorCode {
	switch {
	case errors.Is(err, context.DeadlineExceeded):
		return ErrorTimeout
	case errors.Is(err, context.Canceled):
		return ErrorCanceled
	case errors.Is(err, ErrInventoryBudget):
		return ErrorBudgetExhausted
	case errors.Is(err, ErrInventoryIncomplete):
		return ErrorListingIncomplete
	case errors.Is(err, ErrInventoryInvalid):
		return ErrorParse
	case errors.Is(err, errCapabilityUnavailable):
		return ErrorUnavailable
	default:
		return ErrorRead
	}
}
