package audit

import (
	"fmt"
	"testing"
	"time"
)

func TestSelectMediaSampleIsDeterministicBoundedAndCountsInvalidTimestamps(t *testing.T) {
	now := time.Date(2026, 7, 14, 12, 0, 0, 0, time.UTC)
	records := []ArchivedMediaRecord{{Key: "invalid", SizeBytes: 1}}
	for i := 0; i < 510; i++ {
		records = append(records, ArchivedMediaRecord{Key: fmt.Sprintf("recent-%03d", i), ArchivedAt: now.Add(-time.Duration(i) * time.Minute), ArchivedAtValid: true})
	}
	for i := 0; i < 110; i++ {
		records = append(records, ArchivedMediaRecord{Key: fmt.Sprintf("older-%03d", i), ArchivedAt: now.Add(-8*24*time.Hour - time.Duration(i)*time.Minute), ArchivedAtValid: true})
	}
	first := SelectMediaSample(records, 7*24*time.Hour, now, "cloudflare_r2")
	second := SelectMediaSample(records, 7*24*time.Hour, now, "cloudflare_r2")
	if first.InvalidCount != 1 || first.RecentPopulation != 510 || first.RecentChecked != 500 || first.OlderPopulation != 110 || first.OlderChecked != 100 {
		t.Fatalf("sample counts = %#v", first)
	}
	if first.Mode != "bounded_sample" || first.Confidence != ConfidenceModerate || len(first.Records) != 600 {
		t.Fatalf("sample = %#v", first)
	}
	for i := range first.Records {
		if first.Records[i].Key != second.Records[i].Key {
			t.Fatalf("selection changed at %d: %q != %q", i, first.Records[i].Key, second.Records[i].Key)
		}
	}
}

func TestSelectMediaSampleRecentTieBreakUsesKeyHash(t *testing.T) {
	now := time.Date(2026, 7, 14, 12, 0, 0, 0, time.UTC)
	records := []ArchivedMediaRecord{{Key: "a", ArchivedAt: now, ArchivedAtValid: true}, {Key: "b", ArchivedAt: now, ArchivedAtValid: true}}
	got := SelectMediaSample(records, time.Hour, now, "provider")
	wantFirst := "a"
	if left, right := keyHash("a"), keyHash("b"); string(left[:]) > string(right[:]) {
		wantFirst = "b"
	}
	if got.Records[0].Key != wantFirst {
		t.Fatalf("first key = %q, want %q", got.Records[0].Key, wantFirst)
	}
}
