package retrieval

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestRetrievalLaneSemanticProvenanceIsStructuredAndCompatible(t *testing.T) {
	distance := 0.0
	lane := RetrievalLane{
		Name: "semantic", Status: "used", Rank: 1, RawDistance: &distance,
		Profile: "profile-a", Backend: "exact", Generation: "generation-a",
	}
	payload, err := json.Marshal(lane)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{`"rank":1`, `"raw_distance":0`, `"profile":"profile-a"`, `"backend":"exact"`, `"generation":"generation-a"`} {
		if !strings.Contains(string(payload), want) {
			t.Fatalf("lane JSON missing %s: %s", want, payload)
		}
	}
	legacy, err := json.Marshal(RetrievalLane{Name: "lexical", Status: "used"})
	if err != nil {
		t.Fatal(err)
	}
	for _, absent := range []string{"rank", "raw_distance", "profile", "backend", "generation"} {
		if strings.Contains(string(legacy), absent) {
			t.Fatalf("legacy lane gained empty %s field: %s", absent, legacy)
		}
	}
}

func TestFusionProvenanceJSONCompatibilityAndExactZero(t *testing.T) {
	legacy, err := json.Marshal(RetrievalInfo{Score: 7, Lanes: []RetrievalLane{{Name: "lexical", Status: "used"}}})
	if err != nil {
		t.Fatal(err)
	}
	for _, absent := range []string{"raw_score", "contribution", "fused_score"} {
		if strings.Contains(string(legacy), absent) {
			t.Fatalf("legacy JSON gained %s: %s", absent, legacy)
		}
	}
	zero := 0.0
	payload, err := json.Marshal(RetrievalInfo{FusedScore: &zero, Lanes: []RetrievalLane{{Name: "lexical", RawScore: &zero, Contribution: &zero}}})
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{`"raw_score":0`, `"contribution":0`, `"fused_score":0`} {
		if !strings.Contains(string(payload), want) {
			t.Fatalf("fusion JSON missing %s: %s", want, payload)
		}
	}
}

func TestEvidenceChunkStableProvenanceRoundTrip(t *testing.T) {
	want := EvidenceChunk{ID: "chunk-1", ParentSourceKey: "item:1", Index: 2, SectionOrdinal: 3, ContributingIDs: []string{"chunk-1", "chunk-2"}, WindowHash: "window"}
	payload, err := json.Marshal(want)
	if err != nil {
		t.Fatal(err)
	}
	var got EvidenceChunk
	if err := json.Unmarshal(payload, &got); err != nil {
		t.Fatal(err)
	}
	if got.ID != want.ID || got.SectionOrdinal != want.SectionOrdinal || got.WindowHash != want.WindowHash || strings.Join(got.ContributingIDs, ",") != "chunk-1,chunk-2" {
		t.Fatalf("round trip got %+v want %+v", got, want)
	}
}

func TestWindowHashIsLengthPrefixedAndDeterministic(t *testing.T) {
	got := WindowHash([]string{"a"}, []string{"bc"}, "d")
	if got == "" || got != WindowHash([]string{"a"}, []string{"bc"}, "d") || got == WindowHash([]string{"ab"}, []string{"c"}, "d") {
		t.Fatalf("unexpected hashes %q", got)
	}
}
