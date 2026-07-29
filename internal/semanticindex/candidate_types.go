package semanticindex

// HNSWNode identifies one graph vector by its dense, segment-local ordinal.
// The legacy name is shared by the rejected HNSW bakeoff and the optional
// USearch candidate so their content-free test harness uses one contract.
type HNSWNode struct {
	Ordinal uint64
	Vector  []float32
}

// HNSWHit is an approximate graph candidate. Callers must exactly rerank and
// validate it against authoritative SQLite state before it becomes evidence.
type HNSWHit struct {
	Ordinal  uint64
	Distance float32
}
