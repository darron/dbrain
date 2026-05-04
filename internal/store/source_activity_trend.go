package store

import (
	"fmt"
	"time"
)

func sourceActivityTrendBucket(window time.Duration) time.Duration {
	switch {
	case window <= 12*time.Hour:
		return time.Hour
	case window <= 24*time.Hour:
		return 2 * time.Hour
	case window <= 72*time.Hour:
		return 6 * time.Hour
	case window <= 168*time.Hour:
		return 12 * time.Hour
	default:
		return 24 * time.Hour
	}
}

func buildSourceActivityTrend(rows rowScanner, now time.Time, window time.Duration, bucket time.Duration) ([]SourceActivityTrendPoint, error) {
	if bucket <= 0 {
		bucket = time.Hour
	}
	end := now.UTC().Truncate(bucket).Add(bucket)
	bucketCount := int(window / bucket)
	if window%bucket != 0 {
		bucketCount++
	}
	if bucketCount < 1 {
		bucketCount = 1
	}
	start := end.Add(-time.Duration(bucketCount) * bucket)

	points := make([]SourceActivityTrendPoint, bucketCount)
	indexByBucket := make(map[time.Time]int, bucketCount)
	for i := 0; i < bucketCount; i++ {
		bucketStart := start.Add(time.Duration(i) * bucket)
		points[i] = SourceActivityTrendPoint{
			BucketStart: bucketStart,
			Label:       sourceActivityTrendLabel(bucketStart, bucket),
		}
		indexByBucket[bucketStart] = i
	}

	for rows.Next() {
		var eventClass string
		var eventAtRaw string
		if err := rows.Scan(&eventClass, &eventAtRaw); err != nil {
			return nil, fmt.Errorf("scan source activity trend row: %w", err)
		}
		eventAt := parseStoredTime(eventAtRaw)
		if eventAt.IsZero() || eventAt.Before(start) || !eventAt.Before(end) {
			continue
		}
		bucketStart := eventAt.UTC().Truncate(bucket)
		index, ok := indexByBucket[bucketStart]
		if !ok {
			continue
		}
		if eventClass == "success" {
			points[index].SuccessCount++
		} else {
			points[index].FailureCount++
		}
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate source activity trend rows: %w", err)
	}
	return points, nil
}

func sourceActivityTrendLabel(bucketStart time.Time, bucket time.Duration) string {
	if bucket >= 24*time.Hour {
		return bucketStart.Format("Jan 2")
	}
	if bucket >= 6*time.Hour {
		return bucketStart.Format("Jan 2 15:04")
	}
	return bucketStart.Format("15:04")
}
