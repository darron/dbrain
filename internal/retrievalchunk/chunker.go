package retrievalchunk

import (
	"fmt"
	"strings"
	"unicode"
)

func Build(parent Parent, opts Options) ([]Chunk, error) {
	if strings.TrimSpace(parent.Kind) == "" {
		return nil, fmt.Errorf("parent kind is required")
	}
	if strings.TrimSpace(parent.SourceKey) == "" {
		return nil, fmt.Errorf("parent source key is required")
	}
	if opts.TargetRunes <= 0 || opts.MaxRunes < opts.TargetRunes {
		return nil, fmt.Errorf("invalid chunk sizes: target=%d max=%d", opts.TargetRunes, opts.MaxRunes)
	}
	if opts.OverlapRunes < 0 || opts.OverlapRunes > defaultOverlapRunes || opts.OverlapRunes > opts.TargetRunes {
		return nil, fmt.Errorf("invalid overlap: %d", opts.OverlapRunes)
	}

	chunks := make([]Chunk, 0)
	ordinal := 0
	for _, section := range parent.Sections {
		if strings.TrimSpace(section.Role) == "" {
			return nil, fmt.Errorf("section evidence role is required")
		}
		if strings.TrimSpace(section.Text) == "" {
			continue
		}
		sectionChunks := chunkSection(parent, section, opts, ordinal)
		chunks = append(chunks, sectionChunks...)
		ordinal += len(sectionChunks)
	}
	return chunks, nil
}

func chunkSection(parent Parent, section Section, opts Options, firstOrdinal int) []Chunk {
	runes := []rune(section.Text)
	paragraphs := paragraphBoundaries(runes)
	chunks := make([]Chunk, 0, len(runes)/opts.TargetRunes+1)
	start := 0
	for start < len(runes) {
		end := chooseEnd(runes, paragraphs, start, opts)
		text := string(runes[start:end])
		hash := textHash(text)
		ordinal := firstOrdinal + len(chunks)
		chunks = append(chunks, Chunk{
			ID:               chunkID(parent, section.Role, ordinal, hash),
			ParentKind:       parent.Kind,
			ParentSourceKey:  parent.SourceKey,
			EvidenceRole:     section.Role,
			Ordinal:          ordinal,
			StartChar:        start,
			EndChar:          end,
			Heading:          section.Heading,
			ChunkerVersion:   Version,
			InputContentHash: parent.ContentHash,
			TextHash:         hash,
			Text:             text,
		})
		if end == len(runes) {
			break
		}
		start = overlapStart(runes, paragraphs, start, end, opts.OverlapRunes)
	}
	return chunks
}

type boundaries struct {
	starts []int
	ends   []int
}

func paragraphBoundaries(text []rune) boundaries {
	result := boundaries{starts: []int{0}}
	for i := 0; i < len(text); {
		if text[i] != '\n' {
			i++
			continue
		}
		begin := i
		for i < len(text) && text[i] == '\n' {
			i++
		}
		if i-begin >= 2 {
			result.ends = append(result.ends, i)
			result.starts = append(result.starts, i)
		}
	}
	return result
}

func chooseEnd(text []rune, paragraphs boundaries, start int, opts Options) int {
	remaining := len(text) - start
	if remaining <= opts.MaxRunes {
		return len(text)
	}
	target := start + opts.TargetRunes
	hard := min(start+opts.MaxRunes, len(text))
	if end := lastBoundary(paragraphs.ends, start, target); end > start {
		return end
	}
	if end := firstBoundary(paragraphs.ends, target, hard); end > start {
		return end
	}
	if end := sentenceBoundary(text, start, target); end > start {
		return end
	}
	if end := whitespaceBoundary(text, start, target); end > start {
		return end
	}
	return target
}

func overlapStart(text []rune, paragraphs boundaries, currentStart, end, overlap int) int {
	if overlap == 0 {
		return end
	}
	earliest := max(currentStart+1, end-overlap)
	if start := firstBoundary(paragraphs.starts, earliest, end-1); start >= earliest {
		return start
	}
	for i := earliest; i < end; i++ {
		if unicode.IsSpace(text[i]) {
			return i + 1
		}
	}
	return earliest
}

func sentenceBoundary(text []rune, start, limit int) int {
	for i := limit - 1; i > start; i-- {
		if (text[i] == '.' || text[i] == '!' || text[i] == '?') &&
			(i+1 == len(text) || unicode.IsSpace(text[i+1])) {
			return i + 1
		}
	}
	return 0
}

func whitespaceBoundary(text []rune, start, limit int) int {
	for i := limit - 1; i > start; i-- {
		if unicode.IsSpace(text[i]) {
			return i + 1
		}
	}
	return 0
}

func lastBoundary(values []int, after, atMost int) int {
	result := 0
	for _, value := range values {
		if value > atMost {
			break
		}
		if value > after {
			result = value
		}
	}
	return result
}

func firstBoundary(values []int, atLeast, atMost int) int {
	for _, value := range values {
		if value >= atLeast && value <= atMost {
			return value
		}
	}
	return 0
}
