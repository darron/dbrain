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
