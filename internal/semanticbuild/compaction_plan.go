package semanticbuild

import (
	"fmt"
	"sort"
)

const (
	segmentCompactionMinimum = 5_000
	segmentCompactionMaximum = 200_000
)

type SegmentCompactionClass string

const (
	SegmentCompactionClassExactL0 SegmentCompactionClass = "exact_l0"
	SegmentCompactionClass5K      SegmentCompactionClass = "5k"
	SegmentCompactionClass10K     SegmentCompactionClass = "10k"
	SegmentCompactionClass20K     SegmentCompactionClass = "20k"
	SegmentCompactionClass40K     SegmentCompactionClass = "40k"
	SegmentCompactionClass80K     SegmentCompactionClass = "80k"
	SegmentCompactionClassCapped  SegmentCompactionClass = "capped"
	SegmentCompactionClassInvalid SegmentCompactionClass = "invalid"
)

type SegmentCompactionKind string

const (
	SegmentCompactionNone      SegmentCompactionKind = "none"
	SegmentCompactionSingleton SegmentCompactionKind = "singleton"
	SegmentCompactionPair      SegmentCompactionKind = "pair"
)

type SegmentCompactionInput struct {
	SegmentHash               string
	CreatedOrder              int64
	LiveCount, TombstoneCount int
}

type SegmentCompactionOutput struct {
	LiveCount int
	Class     SegmentCompactionClass
}

type SegmentCompactionPlan struct {
	Kind    SegmentCompactionKind
	Inputs  []SegmentCompactionInput
	Outputs []SegmentCompactionOutput
}

// ClassifySegmentCompactionCount maps an already-filtered live membership
// count to its storage tier. Counts below the minimum belong in exact L0; a
// physical ANN segment never exceeds the capped upper bound.
func ClassifySegmentCompactionCount(live int) SegmentCompactionClass {
	switch {
	case live < segmentCompactionMinimum:
		return SegmentCompactionClassExactL0
	case live < 10_000:
		return SegmentCompactionClass5K
	case live < 20_000:
		return SegmentCompactionClass10K
	case live < 40_000:
		return SegmentCompactionClass20K
	case live < 80_000:
		return SegmentCompactionClass40K
	case live < 160_000:
		return SegmentCompactionClass80K
	case live <= segmentCompactionMaximum:
		return SegmentCompactionClassCapped
	default:
		return SegmentCompactionClassInvalid
	}
}

// PlanSegmentCompaction chooses one bounded maintenance action from immutable
// segment facts. It deliberately does not inspect vectors, SQLite membership,
// or cache paths; later orchestration must revalidate this plan before writing.
func PlanSegmentCompaction(inputs []SegmentCompactionInput) (SegmentCompactionPlan, error) {
	sorted, err := validateAndSortSegmentCompactionInputs(inputs)
	if err != nil {
		return SegmentCompactionPlan{}, err
	}
	for _, input := range sorted {
		if tombstoneRatioOverOnePercent(input) {
			return segmentCompactionPlan(SegmentCompactionSingleton, []SegmentCompactionInput{input}), nil
		}
	}
	byClass := make(map[SegmentCompactionClass][]SegmentCompactionInput)
	for _, input := range sorted {
		class := ClassifySegmentCompactionCount(input.LiveCount)
		if class == SegmentCompactionClassExactL0 || class == SegmentCompactionClassCapped {
			continue
		}
		byClass[class] = append(byClass[class], input)
	}
	for _, class := range []SegmentCompactionClass{
		SegmentCompactionClass5K, SegmentCompactionClass10K, SegmentCompactionClass20K,
		SegmentCompactionClass40K, SegmentCompactionClass80K,
	} {
		if candidates := byClass[class]; len(candidates) >= 2 {
			return segmentCompactionPlan(SegmentCompactionPair, candidates[:2]), nil
		}
	}
	return SegmentCompactionPlan{Kind: SegmentCompactionNone, Inputs: []SegmentCompactionInput{}, Outputs: []SegmentCompactionOutput{}}, nil
}

func validateAndSortSegmentCompactionInputs(inputs []SegmentCompactionInput) ([]SegmentCompactionInput, error) {
	sorted := append([]SegmentCompactionInput(nil), inputs...)
	sort.Slice(sorted, func(i, j int) bool { return sorted[i].CreatedOrder < sorted[j].CreatedOrder })
	seen := make(map[string]struct{}, len(sorted))
	for index, input := range sorted {
		if input.SegmentHash == "" || input.LiveCount <= 0 || input.LiveCount > segmentCompactionMaximum || input.TombstoneCount < 0 {
			return nil, fmt.Errorf("semantic compaction input %d is invalid", index)
		}
		if _, exists := seen[input.SegmentHash]; exists {
			return nil, fmt.Errorf("semantic compaction segment %s is duplicated", input.SegmentHash)
		}
		seen[input.SegmentHash] = struct{}{}
		if index > 0 && input.CreatedOrder == sorted[index-1].CreatedOrder {
			return nil, fmt.Errorf("semantic compaction creation order %d is duplicated", input.CreatedOrder)
		}
	}
	return sorted, nil
}

func tombstoneRatioOverOnePercent(input SegmentCompactionInput) bool {
	return int64(input.TombstoneCount)*100 > int64(input.LiveCount+input.TombstoneCount)
}

func segmentCompactionPlan(kind SegmentCompactionKind, inputs []SegmentCompactionInput) SegmentCompactionPlan {
	total := 0
	for _, input := range inputs {
		total += input.LiveCount
	}
	outputs := segmentCompactionOutputs(total)
	return SegmentCompactionPlan{Kind: kind, Inputs: append([]SegmentCompactionInput(nil), inputs...), Outputs: outputs}
}

func segmentCompactionOutputs(total int) []SegmentCompactionOutput {
	if total > segmentCompactionMaximum {
		capped := min(segmentCompactionMaximum, total-segmentCompactionMinimum)
		return []SegmentCompactionOutput{
			{LiveCount: capped, Class: SegmentCompactionClassCapped},
			{LiveCount: total - capped, Class: ClassifySegmentCompactionCount(total - capped)},
		}
	}
	return []SegmentCompactionOutput{{LiveCount: total, Class: ClassifySegmentCompactionCount(total)}}
}
