package mcpserver

import (
	"fmt"
	"strings"

	"github.com/darron/dbrain/internal/store"
)

func formatCountBuckets(groupBy string, buckets []store.CountBucket) string {
	if len(buckets) == 0 {
		if strings.TrimSpace(groupBy) == "none" {
			return "Count: 0"
		}
		return "Total: 0"
	}
	var b strings.Builder
	total := 0
	grouped := strings.TrimSpace(groupBy) != "none"
	for _, bucket := range buckets {
		total += bucket.Count
		if !grouped {
			continue
		}
		b.WriteString(displayBucketKey(groupBy, bucket.Key))
		b.WriteString(": ")
		_, _ = fmt.Fprintf(&b, "%d", bucket.Count)
		b.WriteString("\n")
	}
	if grouped {
		b.WriteString("Total: ")
		_, _ = fmt.Fprintf(&b, "%d", total)
		return strings.TrimSpace(b.String())
	}
	return fmt.Sprintf("Count: %d", total)
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

func countBucketTotal(buckets []store.CountBucket) int {
	total := 0
	for _, bucket := range buckets {
		total += bucket.Count
	}
	return total
}
