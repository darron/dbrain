package researchhybrid

import (
	"math"
	"sort"
	"strings"

	"github.com/darron/dbrain/internal/retrieval"
)

const (
	LaneLexical  = "lexical"
	LaneSemantic = "semantic"
	LaneExactTag = "exact_tag"

	StatusUsed     = "used"
	StatusDisabled = "disabled"

	DefaultRRFK                 = 60.0
	DefaultLexicalWeight        = 1.0
	DefaultSemanticWeight       = 1.0
	DefaultLexicalDepth         = 50
	DefaultSemanticDepth        = 50
	DefaultFusedCandidateWindow = 20
	DefaultMaxChunksPerParent   = 3
)

type Options struct {
	UseSemantic     bool
	DisableSemantic bool
}

func LaneStatuses(opts Options) []retrieval.RetrievalLane {
	lanes := []retrieval.RetrievalLane{{
		Name:   LaneLexical,
		Status: StatusUsed,
	}}
	semantic := retrieval.RetrievalLane{
		Name:   LaneSemantic,
		Status: StatusDisabled,
	}
	switch {
	case opts.DisableSemantic:
		semantic.Reason = "disabled_for_lexical_debugging"
	case opts.UseSemantic:
		semantic.Reason = "not_configured"
	default:
		semantic.Reason = "not_requested"
	}
	return append(lanes, semantic)
}

func Merge(lexical []retrieval.EvidenceDocument, semantic []retrieval.EvidenceDocument, limit int) []retrieval.EvidenceDocument {
	if limit <= 0 {
		limit = len(lexical) + len(semantic)
	}
	out := make([]retrieval.EvidenceDocument, 0, min(limit, len(lexical)+len(semantic)))
	byKey := map[string]int{}
	for _, row := range lexical {
		row = WithLane(row, retrieval.RetrievalLane{Name: LaneLexical, Status: StatusUsed})
		key := strings.TrimSpace(row.SourceKey)
		if key == "" {
			continue
		}
		if _, exists := byKey[key]; exists {
			continue
		}
		byKey[key] = len(out)
		out = append(out, row)
		if len(out) >= limit {
			return out
		}
	}
	for _, row := range semantic {
		row = WithLane(row, retrieval.RetrievalLane{Name: LaneSemantic, Status: StatusUsed})
		key := strings.TrimSpace(row.SourceKey)
		if key == "" {
			continue
		}
		if idx, exists := byKey[key]; exists {
			out[idx] = mergeLanes(out[idx], row)
			continue
		}
		byKey[key] = len(out)
		out = append(out, row)
		if len(out) >= limit {
			return out
		}
	}
	return out
}

// Fuse combines ranked lexical and semantic passages with reciprocal-rank
// fusion. An unusable semantic lane is an identity operation by contract.
func Fuse(lexical, semantic []retrieval.EvidenceDocument, limit, charBudget int, protected map[string]struct{}) []retrieval.EvidenceDocument {
	usableSemantic := false
	for _, row := range semantic {
		if chunkIdentity(row) != "" {
			usableSemantic = true
			break
		}
	}
	if !usableSemantic {
		return lexical
	}
	type candidate struct {
		doc                       retrieval.EvidenceDocument
		lexicalRank, semanticRank int
		bestRank                  int
		protected                 bool
	}
	byID := make(map[string]*candidate)
	orderedKeys := make([]string, 0, min(len(lexical), DefaultLexicalDepth)+min(len(semantic), DefaultSemanticDepth))
	add := func(doc retrieval.EvidenceDocument, laneName string, rank int, weight float64) {
		key := chunkIdentity(doc)
		if key == "" {
			return
		}
		entry, ok := byID[key]
		if !ok && laneName == LaneSemantic && doc.Chunk != nil {
			parentKey := "parent:" + strings.TrimSpace(doc.SourceKey)
			if parentEntry, exists := byID[parentKey]; exists {
				entry, ok = parentEntry, true
				entry.doc = mergeDocuments(cloneDocument(doc), entry.doc)
				delete(byID, parentKey)
				byID[key] = entry
				for i := range orderedKeys {
					if orderedKeys[i] == parentKey {
						orderedKeys[i] = key
						break
					}
				}
			}
		}
		if !ok {
			copyDoc := cloneDocument(doc)
			entry = &candidate{doc: copyDoc, bestRank: math.MaxInt, protected: isProtected(copyDoc, protected)}
			byID[key] = entry
			orderedKeys = append(orderedKeys, key)
		} else {
			entry.doc = mergeDocuments(entry.doc, doc)
		}
		contribution := weight / (DefaultRRFK + float64(rank))
		lane := laneFor(entry.doc, laneName)
		lane.Name, lane.Status, lane.Rank, lane.Contribution = laneName, StatusUsed, rank, &contribution
		if laneName == LaneLexical {
			entry.lexicalRank = rank
			if lane.RawScore == nil {
				raw := float64(docScore(doc))
				lane.RawScore = &raw
			}
		} else {
			entry.semanticRank = rank
		}
		setLane(&entry.doc, lane)
		if entry.doc.Retrieval == nil {
			entry.doc.Retrieval = &retrieval.RetrievalInfo{}
		}
		if entry.doc.Retrieval.FusedScore == nil {
			zero := 0.0
			entry.doc.Retrieval.FusedScore = &zero
		}
		*entry.doc.Retrieval.FusedScore += contribution
		if rank < entry.bestRank {
			entry.bestRank = rank
		}
	}
	for i, doc := range lexical[:min(len(lexical), DefaultLexicalDepth)] {
		add(doc, LaneLexical, i+1, DefaultLexicalWeight)
	}
	for i, doc := range semantic[:min(len(semantic), DefaultSemanticDepth)] {
		add(doc, LaneSemantic, i+1, DefaultSemanticWeight)
	}
	for _, entry := range byID {
		entry.protected = isProtected(entry.doc, protected)
	}
	candidates := make([]*candidate, 0, len(orderedKeys))
	for _, key := range orderedKeys {
		candidates = append(candidates, byID[key])
	}
	sort.SliceStable(candidates, func(i, j int) bool {
		a, b := candidates[i], candidates[j]
		as, bs := *a.doc.Retrieval.FusedScore, *b.doc.Retrieval.FusedScore
		if as != bs {
			return as > bs
		}
		if a.protected != b.protected {
			return a.protected
		}
		if a.bestRank != b.bestRank {
			return a.bestRank < b.bestRank
		}
		if cmp := compareRank(a.lexicalRank, b.lexicalRank); cmp != 0 {
			return cmp < 0
		}
		if cmp := compareRank(a.semanticRank, b.semanticRank); cmp != 0 {
			return cmp < 0
		}
		if a.doc.SourceKey != b.doc.SourceKey {
			return a.doc.SourceKey < b.doc.SourceKey
		}
		return primaryChunkID(a.doc) < primaryChunkID(b.doc)
	})
	rows := make([]retrieval.EvidenceDocument, 0, len(candidates))
	for _, candidate := range candidates {
		rows = append(rows, candidate.doc)
	}
	rows = Consolidate(rows, charBudget, protected)
	resolved := limit
	if resolved <= 0 {
		resolved = len(rows)
	}
	resolved = min(resolved, DefaultFusedCandidateWindow)
	if len(rows) > resolved {
		rows = retainProtected(rows, resolved, protected)
	}
	return rows
}

func retainProtected(rows []retrieval.EvidenceDocument, limit int, protected map[string]struct{}) []retrieval.EvidenceDocument {
	if limit <= 0 {
		return []retrieval.EvidenceDocument{}
	}
	selected := append([]retrieval.EvidenceDocument(nil), rows[:min(limit, len(rows))]...)
	for _, row := range rows[limit:] {
		if !isProtected(row, protected) {
			continue
		}
		replace := -1
		for i := len(selected) - 1; i >= 0; i-- {
			if !isProtected(selected[i], protected) {
				replace = i
				break
			}
		}
		if replace < 0 {
			continue
		}
		selected[replace] = row
	}
	sort.SliceStable(selected, func(i, j int) bool { return documentLess(selected[i], selected[j], protected) })
	return selected
}

func compareRank(a, b int) int {
	if a == b {
		return 0
	}
	if a == 0 {
		return 1
	}
	if b == 0 {
		return -1
	}
	if a < b {
		return -1
	}
	return 1
}
func primaryChunkID(doc retrieval.EvidenceDocument) string {
	if doc.Chunk != nil {
		return strings.TrimSpace(doc.Chunk.ID)
	}
	return ""
}
func chunkIdentity(doc retrieval.EvidenceDocument) string {
	if id := primaryChunkID(doc); id != "" {
		return "chunk:" + id
	}
	if key := strings.TrimSpace(doc.SourceKey); key != "" {
		return "parent:" + key
	}
	return ""
}
func docScore(doc retrieval.EvidenceDocument) int {
	if doc.Retrieval != nil {
		return doc.Retrieval.Score
	}
	return 0
}
func isProtected(doc retrieval.EvidenceDocument, protected map[string]struct{}) bool {
	_, ok := protected[strings.TrimSpace(doc.SourceKey)]
	if ok {
		return true
	}
	if doc.Retrieval != nil {
		for _, lane := range doc.Retrieval.Lanes {
			if strings.EqualFold(strings.TrimSpace(lane.Name), LaneExactTag) {
				return true
			}
		}
		for _, s := range doc.Retrieval.Signals {
			if strings.Contains(strings.ToLower(s.Name), "exact") || strings.Contains(strings.ToLower(s.Name), "protected") {
				return true
			}
		}
	}
	return false
}

func cloneDocument(doc retrieval.EvidenceDocument) retrieval.EvidenceDocument {
	if doc.Chunk != nil {
		c := *doc.Chunk
		c.ContributingIDs = append([]string(nil), doc.Chunk.ContributingIDs...)
		doc.Chunk = &c
	}
	if doc.Retrieval != nil {
		r := *doc.Retrieval
		r.Lanes = append([]retrieval.RetrievalLane(nil), r.Lanes...)
		r.Signals = append([]retrieval.RetrievalSignal(nil), r.Signals...)
		r.MatchedTerms = append([]string(nil), r.MatchedTerms...)
		r.MissingTerms = append([]string(nil), r.MissingTerms...)
		if r.FusedScore != nil {
			v := *r.FusedScore
			r.FusedScore = &v
		}
		doc.Retrieval = &r
	}
	return doc
}
func laneFor(doc retrieval.EvidenceDocument, name string) retrieval.RetrievalLane {
	if doc.Retrieval != nil {
		for _, lane := range doc.Retrieval.Lanes {
			if strings.EqualFold(lane.Name, name) {
				return lane
			}
		}
	}
	return retrieval.RetrievalLane{Name: name}
}
func setLane(doc *retrieval.EvidenceDocument, lane retrieval.RetrievalLane) {
	if doc.Retrieval == nil {
		doc.Retrieval = &retrieval.RetrievalInfo{}
	}
	for i := range doc.Retrieval.Lanes {
		if strings.EqualFold(doc.Retrieval.Lanes[i].Name, lane.Name) {
			doc.Retrieval.Lanes[i] = lane
			return
		}
	}
	doc.Retrieval.Lanes = append(doc.Retrieval.Lanes, lane)
}
func mergeDocuments(primary, secondary retrieval.EvidenceDocument) retrieval.EvidenceDocument {
	if secondary.Retrieval != nil {
		if primary.Retrieval == nil {
			primary.Retrieval = &retrieval.RetrievalInfo{}
		}
		if primary.Retrieval.FusedScore == nil && secondary.Retrieval.FusedScore != nil {
			value := *secondary.Retrieval.FusedScore
			primary.Retrieval.FusedScore = &value
		}
		for _, lane := range secondary.Retrieval.Lanes {
			setLane(&primary, lane)
		}
		primary.Retrieval.Signals = append(primary.Retrieval.Signals, secondary.Retrieval.Signals...)
		primary.Retrieval.MatchedTerms = uniqueFold(append(primary.Retrieval.MatchedTerms, secondary.Retrieval.MatchedTerms...))
		primary.Retrieval.MissingTerms = subtractMatched(uniqueFold(append(primary.Retrieval.MissingTerms, secondary.Retrieval.MissingTerms...)), primary.Retrieval.MatchedTerms)
	}
	return primary
}
func subtractMatched(missing, matched []string) []string {
	seen := map[string]struct{}{}
	for _, value := range matched {
		seen[strings.ToLower(strings.TrimSpace(value))] = struct{}{}
	}
	out := missing[:0]
	for _, value := range missing {
		if _, ok := seen[strings.ToLower(strings.TrimSpace(value))]; !ok {
			out = append(out, value)
		}
	}
	return out
}
func uniqueFold(values []string) []string {
	out := make([]string, 0, len(values))
	seen := map[string]struct{}{}
	for _, v := range values {
		k := strings.ToLower(strings.TrimSpace(v))
		if k == "" {
			continue
		}
		if _, ok := seen[k]; ok {
			continue
		}
		seen[k] = struct{}{}
		out = append(out, v)
	}
	return out
}

func WithLane(row retrieval.EvidenceDocument, lane retrieval.RetrievalLane) retrieval.EvidenceDocument {
	lane.Name = strings.TrimSpace(lane.Name)
	if lane.Name == "" {
		return row
	}
	if row.Retrieval == nil {
		row.Retrieval = &retrieval.RetrievalInfo{}
	}
	for _, current := range row.Retrieval.Lanes {
		if strings.EqualFold(current.Name, lane.Name) {
			return row
		}
	}
	row.Retrieval.Lanes = append(row.Retrieval.Lanes, lane)
	return row
}

func WithLaneAll(rows []retrieval.EvidenceDocument, lane retrieval.RetrievalLane) []retrieval.EvidenceDocument {
	out := make([]retrieval.EvidenceDocument, 0, len(rows))
	for _, row := range rows {
		out = append(out, WithLane(row, lane))
	}
	return out
}

func mergeLanes(primary retrieval.EvidenceDocument, secondary retrieval.EvidenceDocument) retrieval.EvidenceDocument {
	if secondary.Retrieval == nil {
		return primary
	}
	for _, lane := range secondary.Retrieval.Lanes {
		primary = WithLane(primary, lane)
	}
	return primary
}

func min(a int, b int) int {
	if a < b {
		return a
	}
	return b
}
