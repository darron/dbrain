package semanticbuild

import "testing"

func TestCompactionClassifiesBoundaries(t *testing.T) {
	t.Parallel()
	for _, test := range []struct {
		live  int
		class SegmentCompactionClass
	}{
		{4_999, SegmentCompactionClassExactL0},
		{5_000, SegmentCompactionClass5K},
		{9_999, SegmentCompactionClass5K},
		{10_000, SegmentCompactionClass10K},
		{159_999, SegmentCompactionClass80K},
		{160_000, SegmentCompactionClassCapped},
		{200_000, SegmentCompactionClassCapped},
	} {
		if got := ClassifySegmentCompactionCount(test.live); got != test.class {
			t.Fatalf("live=%d class=%q want %q", test.live, got, test.class)
		}
	}
}

func TestPlanSegmentCompactionPrefersOldestTombstoneSingleton(t *testing.T) {
	t.Parallel()
	plan, err := PlanSegmentCompaction([]SegmentCompactionInput{
		{SegmentHash: "pair-one", CreatedOrder: 1, LiveCount: 5_000},
		{SegmentHash: "pair-two", CreatedOrder: 2, LiveCount: 5_000},
		{SegmentHash: "cleanup-old", CreatedOrder: 3, LiveCount: 10_000, TombstoneCount: 102},
		{SegmentHash: "cleanup-new", CreatedOrder: 4, LiveCount: 10_000, TombstoneCount: 103},
	})
	if err != nil {
		t.Fatal(err)
	}
	if plan.Kind != SegmentCompactionSingleton || len(plan.Inputs) != 1 || plan.Inputs[0].SegmentHash != "cleanup-old" {
		t.Fatalf("plan = %+v", plan)
	}
}

func TestPlanSegmentCompactionDoesNotCleanExactlyOnePercentTombstones(t *testing.T) {
	t.Parallel()
	plan, err := PlanSegmentCompaction([]SegmentCompactionInput{
		{SegmentHash: "one-percent", CreatedOrder: 1, LiveCount: 9_900, TombstoneCount: 100},
		{SegmentHash: "peer", CreatedOrder: 2, LiveCount: 9_900},
	})
	if err != nil {
		t.Fatal(err)
	}
	if plan.Kind != SegmentCompactionPair {
		t.Fatalf("plan = %+v", plan)
	}
}

func TestPlanSegmentCompactionSelectsOldestSameClassPair(t *testing.T) {
	t.Parallel()
	plan, err := PlanSegmentCompaction([]SegmentCompactionInput{
		{SegmentHash: "third", CreatedOrder: 30, LiveCount: 7_000},
		{SegmentHash: "first", CreatedOrder: 10, LiveCount: 7_000},
		{SegmentHash: "second", CreatedOrder: 20, LiveCount: 7_000},
		{SegmentHash: "other-class", CreatedOrder: 1, LiveCount: 10_000},
	})
	if err != nil {
		t.Fatal(err)
	}
	if plan.Kind != SegmentCompactionPair || len(plan.Inputs) != 2 || plan.Inputs[0].SegmentHash != "first" || plan.Inputs[1].SegmentHash != "second" {
		t.Fatalf("plan = %+v", plan)
	}
	if len(plan.Outputs) != 1 || plan.Outputs[0].LiveCount != 14_000 || plan.Outputs[0].Class != SegmentCompactionClass10K {
		t.Fatalf("outputs = %+v", plan.Outputs)
	}
}

func TestPlanSegmentCompactionRefusesCappedAndMixedClasses(t *testing.T) {
	t.Parallel()
	for _, inputs := range [][]SegmentCompactionInput{
		{{SegmentHash: "capped-one", CreatedOrder: 1, LiveCount: 160_000}, {SegmentHash: "capped-two", CreatedOrder: 2, LiveCount: 160_000}},
		{{SegmentHash: "five", CreatedOrder: 1, LiveCount: 5_000}, {SegmentHash: "ten", CreatedOrder: 2, LiveCount: 10_000}},
	} {
		plan, err := PlanSegmentCompaction(inputs)
		if err != nil {
			t.Fatal(err)
		}
		if plan.Kind != SegmentCompactionNone {
			t.Fatalf("plan = %+v", plan)
		}
	}
}

func TestPlanSegmentCompactionClassifiesUndersizedAndCappedOutputs(t *testing.T) {
	t.Parallel()
	for _, test := range []struct {
		name    string
		inputs  []SegmentCompactionInput
		outputs []SegmentCompactionOutput
	}{
		{
			name:    "singleton remainder enters exact L0",
			inputs:  []SegmentCompactionInput{{SegmentHash: "sparse", CreatedOrder: 1, LiveCount: 4_999, TombstoneCount: 51}},
			outputs: []SegmentCompactionOutput{{LiveCount: 4_999, Class: SegmentCompactionClassExactL0}},
		},
		{
			name:    "upper pair packs capped and lower remainder",
			inputs:  []SegmentCompactionInput{{SegmentHash: "upper-one", CreatedOrder: 1, LiveCount: 100_000}, {SegmentHash: "upper-two", CreatedOrder: 2, LiveCount: 100_001}},
			outputs: []SegmentCompactionOutput{{LiveCount: 195_001, Class: SegmentCompactionClassCapped}, {LiveCount: 5_000, Class: SegmentCompactionClass5K}},
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			plan, err := PlanSegmentCompaction(test.inputs)
			if err != nil {
				t.Fatal(err)
			}
			if len(plan.Outputs) != len(test.outputs) {
				t.Fatalf("outputs = %+v", plan.Outputs)
			}
			for index, want := range test.outputs {
				if got := plan.Outputs[index]; got != want {
					t.Fatalf("output[%d] = %+v want %+v", index, got, want)
				}
			}
		})
	}
}
