package embedding

import (
	"strings"
	"testing"
)

func TestProfileIDIsStableAndCoversEveryCompatibilityField(t *testing.T) {
	t.Parallel()
	base := Profile{
		Provider: "ollama", Model: "nomic-embed-text:latest",
		ProjectionVersion: "retrieval-projection-v1", ChunkerVersion: "retrieval-chunker-v1",
		Representation: RepresentationDenseF32, Normalization: NormalizationL2, Dimensions: 768,
	}
	want, err := base.ID()
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(want, "embedding-profile-v1:") {
		t.Fatalf("profile ID = %q, want versioned prefix", want)
	}
	const canonicalID = "embedding-profile-v1:b061517806aef851d5d74e3d52b351ba46747d52a30af59f61ed06b9c1401e07"
	if want != canonicalID {
		t.Fatalf("profile ID = %q, want persisted canonical ID %q", want, canonicalID)
	}
	again, err := base.ID()
	if err != nil || again != want {
		t.Fatalf("stable profile ID = %q, %v; want %q", again, err, want)
	}

	variants := []Profile{base, base, base, base, base, base}
	variants[0].Provider = "hosted"
	variants[1].Model = "nomic-embed-text:v2"
	variants[2].ProjectionVersion = "retrieval-projection-v2"
	variants[3].ChunkerVersion = "retrieval-chunker-v2"
	variants[4].Normalization = NormalizationNone
	variants[5].Dimensions = 384
	for _, variant := range variants {
		got, err := variant.ID()
		if err != nil {
			t.Fatalf("variant profile ID: %v", err)
		}
		if got == want {
			t.Fatalf("profile field change reused ID %q: %+v", got, variant)
		}
	}
}

func TestProfileRejectsIncompleteOrUnsupportedConfiguration(t *testing.T) {
	t.Parallel()
	valid := Profile{
		Provider: "fake", Model: "fake-v1", ProjectionVersion: "projection-v1",
		ChunkerVersion: "chunker-v1", Representation: RepresentationDenseF32,
		Normalization: NormalizationL2, Dimensions: 2,
	}
	for _, mutate := range []func(*Profile){
		func(p *Profile) { p.Provider = "" },
		func(p *Profile) { p.Model = "" },
		func(p *Profile) { p.ProjectionVersion = "" },
		func(p *Profile) { p.ChunkerVersion = "" },
		func(p *Profile) { p.Representation = "dense_f16" },
		func(p *Profile) { p.Normalization = "unit-ish" },
		func(p *Profile) { p.Dimensions = 0 },
	} {
		profile := valid
		mutate(&profile)
		if _, err := profile.ID(); err == nil {
			t.Fatalf("invalid profile unexpectedly produced an ID: %+v", profile)
		}
	}
}
