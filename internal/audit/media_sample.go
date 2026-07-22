package audit

import (
	"bytes"
	"crypto/sha256"
	"fmt"
	"sort"
	"time"
)

const mediaSampleSeed = "dbrain.audit.media.v1"
const recentMediaCap = 500
const olderMediaCap = 100

type MediaSample struct {
	Records                                                        []ArchivedMediaRecord
	RecentPopulation, RecentChecked, OlderPopulation, OlderChecked int
	InvalidCount                                                   int
	Mode                                                           string
	Confidence                                                     Confidence
}

func SelectMediaSample(records []ArchivedMediaRecord, since time.Duration, now time.Time, provider string) MediaSample {
	cutoff := now.Add(-since)
	recent := make([]ArchivedMediaRecord, 0)
	older := make([]ArchivedMediaRecord, 0)
	for _, record := range records {
		if !record.ArchivedAtValid || record.ArchivedAt.IsZero() {
			continue
		}
		if record.ArchivedAt.Before(cutoff) {
			older = append(older, record)
		} else {
			recent = append(recent, record)
		}
	}
	sort.Slice(recent, func(i, j int) bool {
		if recent[i].ArchivedAt.Equal(recent[j].ArchivedAt) {
			left, right := keyHash(recent[i].Key), keyHash(recent[j].Key)
			return bytes.Compare(left[:], right[:]) < 0
		}
		return recent[i].ArchivedAt.After(recent[j].ArchivedAt)
	})
	year, week := now.UTC().ISOWeek()
	seed := fmt.Sprintf("%s%s%04d-W%02d", mediaSampleSeed, provider, year, week)
	sort.Slice(older, func(i, j int) bool {
		left, right := seededHash(seed, older[i].Key), seededHash(seed, older[j].Key)
		return bytes.Compare(left[:], right[:]) < 0
	})
	sample := MediaSample{RecentPopulation: len(recent), OlderPopulation: len(older), InvalidCount: len(records) - len(recent) - len(older), Mode: "complete", Confidence: ConfidenceHigh}
	if len(recent) > recentMediaCap {
		recent = recent[:recentMediaCap]
		sample.Mode = "bounded_sample"
		sample.Confidence = ConfidenceModerate
	}
	if len(older) > olderMediaCap {
		older = older[:olderMediaCap]
		sample.Mode = "bounded_sample"
		sample.Confidence = ConfidenceModerate
	}
	sample.RecentChecked = len(recent)
	sample.OlderChecked = len(older)
	sample.Records = append(recent, older...)
	return sample
}
func keyHash(key string) [32]byte          { return sha256.Sum256([]byte(key)) }
func seededHash(seed, key string) [32]byte { return sha256.Sum256([]byte(seed + key)) }
