package app

import (
	"fmt"
	"strings"
	"time"

	"github.com/darron/dbrain/internal/store"
)

func writeCountBuckets(dst interface{ Write([]byte) (int, error) }, groupBy string, buckets []store.CountBucket) error {
	total := 0
	grouped := strings.TrimSpace(groupBy) != "none"
	for _, bucket := range buckets {
		total += bucket.Count
		if grouped {
			label := displayBucketKey(groupBy, bucket.Key)
			if _, err := fmt.Fprintf(dst, "%s: %d\n", label, bucket.Count); err != nil {
				return err
			}
			continue
		}
	}
	if grouped {
		_, err := fmt.Fprintf(dst, "Total: %d\n", total)
		return err
	}
	_, err := fmt.Fprintf(dst, "Count: %d\n", total)
	return err
}

func displayBucketKey(groupBy string, key string) string {
	value := strings.TrimSpace(key)
	if value != "" {
		return value
	}
	switch strings.TrimSpace(groupBy) {
	case "summary-status", "extract-status":
		return "pending"
	default:
		return "(empty)"
	}
}

func writeActivityStats(dst interface{ Write([]byte) (int, error) }, stats store.ActivityStats) error {
	lines := []struct {
		label string
		value string
	}{
		{"Now", stats.Now.Format(time.RFC3339)},
		{"Window", stats.Window},
		{"Latest item write", formatActivityTime(stats.Now, stats.LatestItemUpdatedAt)},
		{"Latest source write", formatActivityTime(stats.Now, stats.LatestSourceUpdatedAt)},
		{"Latest source summary", formatActivityTime(stats.Now, stats.LatestSourceSummaryAt)},
		{"Items updated in window", fmt.Sprintf("%d", stats.ItemsUpdatedInWindow)},
		{"Sources updated in window", fmt.Sprintf("%d", stats.SourcesUpdatedInWindow)},
		{"Sources summarized in window", fmt.Sprintf("%d", stats.SourcesSummarizedInWindow)},
	}

	for _, line := range lines {
		if _, err := fmt.Fprintf(dst, "%s: %s\n", line.label, line.value); err != nil {
			return err
		}
	}
	return nil
}

func formatActivityTime(now time.Time, value time.Time) string {
	if value.IsZero() {
		return "never"
	}
	age := now.Sub(value).Round(time.Second)
	if age < 0 {
		age = 0
	}
	return fmt.Sprintf("%s (%s ago)", value.Format(time.RFC3339), age)
}

func writeBacklogStats(dst interface{ Write([]byte) (int, error) }, stats store.BacklogStats) error {
	drained := "no"
	if stats.Drained {
		drained = "yes"
	}

	lines := []struct {
		label string
		value string
	}{
		{"Queue drained", drained},
		{"X hydration pending", fmt.Sprintf("%d", stats.XHydrationPending)},
		{"Link discovery pending", fmt.Sprintf("%d", stats.LinkDiscoveryPending)},
		{"Source extraction pending", fmt.Sprintf("%d", stats.SourceExtractionPending)},
		{"Source summary pending", fmt.Sprintf("%d", stats.SourceSummaryPending)},
	}
	for _, line := range lines {
		if _, err := fmt.Fprintf(dst, "%s: %s\n", line.label, line.value); err != nil {
			return err
		}
	}

	if err := writeOptionalBucketSection(dst, "Source extraction backlog by type", stats.SourceExtractionPendingByType); err != nil {
		return err
	}
	if err := writeOptionalBucketSection(dst, "Source summary backlog by type", stats.SourceSummaryPendingByType); err != nil {
		return err
	}
	return nil
}

func writeOptionalBucketSection(dst interface{ Write([]byte) (int, error) }, title string, buckets []store.CountBucket) error {
	if len(buckets) == 0 {
		return nil
	}
	if _, err := fmt.Fprintf(dst, "%s:\n", title); err != nil {
		return err
	}
	for _, bucket := range buckets {
		if _, err := fmt.Fprintf(dst, "%s: %d\n", displayBucketKey("source-type", bucket.Key), bucket.Count); err != nil {
			return err
		}
	}
	return nil
}
