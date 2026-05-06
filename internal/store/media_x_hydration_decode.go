package store

import (
	"encoding/json"
	"strings"
)

type xHydrationSnapshot struct {
	MediaObjects []xHydrationMedia `json:"media_objects,omitempty"`
}

type xHydrationEnvelope struct {
	Snapshot xHydrationSnapshot `json:"snapshot"`
	Raw      map[string]any     `json:"raw"`
}

type xHydrationMedia struct {
	Type        string `json:"type,omitempty"`
	URL         string `json:"url,omitempty"`
	ExpandedURL string `json:"expanded_url,omitempty"`
	Width       int    `json:"width,omitempty"`
	Height      int    `json:"height,omitempty"`
}

func decodeXHydrationSnapshot(raw string) (xHydrationSnapshot, bool, error) {
	if strings.TrimSpace(raw) == "" {
		return xHydrationSnapshot{}, false, nil
	}

	var envelope xHydrationEnvelope
	if err := json.Unmarshal([]byte(raw), &envelope); err != nil {
		var snapshot xHydrationSnapshot
		if err := json.Unmarshal([]byte(raw), &snapshot); err != nil {
			return xHydrationSnapshot{}, false, err
		}
		if len(snapshot.MediaObjects) > 0 {
			return snapshot, true, nil
		}
		return snapshot, true, nil
	}

	snapshot := enrichXHydrationSnapshotFromRaw(envelope.Snapshot, envelope.Raw)
	if len(snapshot.MediaObjects) > 0 {
		return snapshot, true, nil
	}

	var direct xHydrationSnapshot
	if err := json.Unmarshal([]byte(raw), &direct); err != nil {
		return xHydrationSnapshot{}, false, err
	}
	return direct, true, nil
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return value
		}
	}
	return ""
}

func maxInt(values ...int) int {
	max := 0
	for _, value := range values {
		if value > max {
			max = value
		}
	}
	return max
}

func enrichXHydrationSnapshotFromRaw(snapshot xHydrationSnapshot, raw map[string]any) xHydrationSnapshot {
	rawMedia := extractXHydrationMediaFromRaw(raw)
	if len(rawMedia) == 0 {
		return snapshot
	}
	if len(snapshot.MediaObjects) == 0 {
		snapshot.MediaObjects = rawMedia
		return snapshot
	}
	snapshot.MediaObjects = mergeXHydrationMedia(snapshot.MediaObjects, rawMedia)
	return snapshot
}

func mergeXHydrationMedia(current, raw []xHydrationMedia) []xHydrationMedia {
	if len(raw) == 0 {
		return current
	}
	if len(current) == len(raw) {
		out := make([]xHydrationMedia, 0, len(current))
		for i := range current {
			out = append(out, mergeXHydrationMediaRef(current[i], raw[i]))
		}
		return out
	}

	out := make([]xHydrationMedia, 0, len(current))
	used := make([]bool, len(raw))
	for i, media := range current {
		match := findRawMediaMatch(media, raw, used, i)
		if match >= 0 {
			used[match] = true
			out = append(out, mergeXHydrationMediaRef(media, raw[match]))
			continue
		}
		out = append(out, media)
	}
	return out
}

func findRawMediaMatch(target xHydrationMedia, candidates []xHydrationMedia, used []bool, index int) int {
	if target.ExpandedURL != "" {
		for i, candidate := range candidates {
			if used[i] {
				continue
			}
			if strings.TrimSpace(candidate.ExpandedURL) == strings.TrimSpace(target.ExpandedURL) {
				return i
			}
		}
	}
	if index >= 0 && index < len(candidates) && !used[index] {
		candidate := candidates[index]
		if sameMediaKind(target.Type, candidate.Type) {
			return index
		}
	}
	for i, candidate := range candidates {
		if used[i] {
			continue
		}
		if sameMediaKind(target.Type, candidate.Type) {
			return i
		}
	}
	return -1
}

func mergeXHydrationMediaRef(current, raw xHydrationMedia) xHydrationMedia {
	merged := current
	merged.Type = firstNonEmpty(current.Type, raw.Type)
	merged.ExpandedURL = firstNonEmpty(current.ExpandedURL, raw.ExpandedURL)
	merged.Width = maxInt(current.Width, raw.Width)
	merged.Height = maxInt(current.Height, raw.Height)

	switch merged.Type {
	case "video", "animated_gif":
		if looksPlayableVideoURL(raw.URL) && !looksPlayableVideoURL(current.URL) {
			merged.URL = raw.URL
		} else {
			merged.URL = firstNonEmpty(current.URL, raw.URL)
		}
	default:
		merged.URL = firstNonEmpty(current.URL, raw.URL)
	}
	return merged
}
