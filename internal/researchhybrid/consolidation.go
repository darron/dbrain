package researchhybrid

import (
	"sort"
	"strings"
	"unicode/utf8"

	"github.com/darron/dbrain/internal/retrieval"
)

func Consolidate(rows []retrieval.EvidenceDocument, charBudget int, protected map[string]struct{}) []retrieval.EvidenceDocument {
	out := make([]retrieval.EvidenceDocument, 0, len(rows))
	if len(rows) == 0 {
		return out
	}
	rows = append([]retrieval.EvidenceDocument(nil), rows...)
	sort.SliceStable(rows, func(i, j int) bool { return documentLess(rows[i], rows[j], protected) })
	processed := map[string]struct{}{}
	for _, row := range rows {
		parent := parentIdentity(row)
		if parent == "" {
			out = append(out, cloneDocument(row))
			continue
		}
		if _, ok := processed[parent]; ok {
			continue
		}
		processed[parent] = struct{}{}
		group := make([]retrieval.EvidenceDocument, 0)
		for _, candidate := range rows {
			if parentIdentity(candidate) == parent {
				group = append(group, candidate)
			}
		}
		primary := cloneDocument(group[0])
		if primary.Chunk == nil {
			out = append(out, primary)
			continue
		}
		isAnchor := false
		for _, candidate := range group {
			if isProtected(candidate, protected) {
				isAnchor = true
				break
			}
		}
		maxChunks := DefaultMaxChunksPerParent
		if isAnchor {
			maxChunks = len(group)
		}
		selected := []retrieval.EvidenceDocument{primary}
		adjacent := map[string]struct{}{}
		for _, wantIndex := range []int{primary.Chunk.Index - 1, primary.Chunk.Index + 1} {
			for _, candidate := range group[1:] {
				if candidate.Chunk != nil && candidate.Chunk.Index == wantIndex {
					selected = append(selected, candidate)
					adjacent[chunkIdentity(candidate)] = struct{}{}
					break
				}
			}
		}
		for _, candidate := range group[1:] {
			if len(selected) >= maxChunks {
				break
			}
			if _, ok := adjacent[chunkIdentity(candidate)]; ok {
				continue
			}
			selected = append(selected, candidate)
		}
		if len(selected) > maxChunks {
			selected = selected[:maxChunks]
		}
		mergeable := []retrieval.EvidenceDocument{primary}
		standalone := make([]retrieval.EvidenceDocument, 0)
		for _, candidate := range selected[1:] {
			_, isAdjacent := adjacent[chunkIdentity(candidate)]
			if !isAdjacent || !chunksCompatible(primary, candidate) {
				standalone = append(standalone, cloneDocument(candidate))
				continue
			}
			trial := append(append([]retrieval.EvidenceDocument(nil), mergeable...), candidate)
			if charBudget > 0 && utf8.RuneCountInString(joinedExcerpt(trial)) > charBudget {
				standalone = append(standalone, cloneDocument(candidate))
				continue
			}
			mergeable = trial
		}
		if len(mergeable) > 1 {
			primary = mergeWindow(primary, mergeable)
		}
		out = append(out, primary)
		out = append(out, standalone...)
	}
	sort.SliceStable(out, func(i, j int) bool { return documentLess(out[i], out[j], protected) })
	return out
}

func parentIdentity(row retrieval.EvidenceDocument) string {
	if row.Chunk != nil && strings.TrimSpace(row.Chunk.ParentSourceKey) != "" {
		return strings.TrimSpace(row.Chunk.ParentSourceKey)
	}
	return strings.TrimSpace(row.SourceKey)
}
func chunksCompatible(a, b retrieval.EvidenceDocument) bool {
	return a.Chunk != nil && b.Chunk != nil && parentIdentity(a) == parentIdentity(b) && strings.EqualFold(strings.TrimSpace(a.EvidenceRole), strings.TrimSpace(b.EvidenceRole)) && a.Chunk.SectionOrdinal == b.Chunk.SectionOrdinal
}
func mergeWindow(primary retrieval.EvidenceDocument, rows []retrieval.EvidenceDocument) retrieval.EvidenceDocument {
	sort.Slice(rows, func(i, j int) bool {
		if rows[i].Chunk.StartChar != rows[j].Chunk.StartChar {
			return rows[i].Chunk.StartChar < rows[j].Chunk.StartChar
		}
		return rows[i].Chunk.ID < rows[j].Chunk.ID
	})
	ids := make([]string, 0, len(rows))
	hashes := make([]string, 0, len(rows))
	for _, row := range rows {
		ids = append(ids, row.Chunk.ID)
		hashes = append(hashes, row.Chunk.Hash)
	}
	primary.Excerpt = joinedExcerpt(rows)
	primary.Chunk.StartChar = rows[0].Chunk.StartChar
	primary.Chunk.EndChar = rows[len(rows)-1].Chunk.EndChar
	primary.Chunk.ContributingIDs = ids
	primary.Chunk.WindowHash = retrieval.WindowHash(ids, hashes, primary.Excerpt)
	return primary
}
func joinedExcerpt(rows []retrieval.EvidenceDocument) string {
	sort.Slice(rows, func(i, j int) bool {
		if rows[i].Chunk.StartChar != rows[j].Chunk.StartChar {
			return rows[i].Chunk.StartChar < rows[j].Chunk.StartChar
		}
		return rows[i].Chunk.ID < rows[j].Chunk.ID
	})
	var b strings.Builder
	end := rows[0].Chunk.StartChar
	for i, row := range rows {
		runes := []rune(row.Excerpt)
		start := row.Chunk.StartChar
		if i > 0 && start > end {
			b.WriteByte('\n')
		}
		skip := max(0, end-start)
		if skip < len(runes) {
			b.WriteString(string(runes[skip:]))
		}
		if row.Chunk.EndChar > end {
			end = row.Chunk.EndChar
		}
	}
	return b.String()
}
func documentLess(a, b retrieval.EvidenceDocument, protected map[string]struct{}) bool {
	as, bs := fusedValue(a), fusedValue(b)
	if as != bs {
		return as > bs
	}
	ap, bp := isProtected(a, protected), isProtected(b, protected)
	if ap != bp {
		return ap
	}
	ab, bb := bestLaneRank(a), bestLaneRank(b)
	if ab != bb {
		return compareRank(ab, bb) < 0
	}
	al, bl := namedLaneRank(a, LaneLexical), namedLaneRank(b, LaneLexical)
	if al != bl {
		return compareRank(al, bl) < 0
	}
	asem, bsem := namedLaneRank(a, LaneSemantic), namedLaneRank(b, LaneSemantic)
	if asem != bsem {
		return compareRank(asem, bsem) < 0
	}
	if a.SourceKey != b.SourceKey {
		return a.SourceKey < b.SourceKey
	}
	return primaryChunkID(a) < primaryChunkID(b)
}
func fusedValue(row retrieval.EvidenceDocument) float64 {
	if row.Retrieval != nil && row.Retrieval.FusedScore != nil {
		return *row.Retrieval.FusedScore
	}
	return 0
}
func namedLaneRank(row retrieval.EvidenceDocument, name string) int {
	if row.Retrieval != nil {
		for _, lane := range row.Retrieval.Lanes {
			if strings.EqualFold(lane.Name, name) {
				return lane.Rank
			}
		}
	}
	return 0
}
func bestLaneRank(row retrieval.EvidenceDocument) int {
	best := 0
	if row.Retrieval != nil {
		for _, lane := range row.Retrieval.Lanes {
			if lane.Rank > 0 && (best == 0 || lane.Rank < best) {
				best = lane.Rank
			}
		}
	}
	return best
}
