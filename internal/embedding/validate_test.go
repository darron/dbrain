package embedding

import "testing"

func TestValidateEncodedVectorEnforcesRepresentationDimensionsAndNormalization(t *testing.T) {
	t.Parallel()
	valid := EncodeDenseF32([]float32{0.6, 0.8})
	if err := ValidateEncodedVector(valid, 2, RepresentationDenseF32, NormalizationL2); err != nil {
		t.Fatalf("valid encoded vector: %v", err)
	}
	for _, tc := range []struct {
		name           string
		bytes          []byte
		dimensions     int
		representation string
		normalization  string
	}{
		{name: "wrong length", bytes: valid[:4], dimensions: 2, representation: RepresentationDenseF32, normalization: NormalizationL2},
		{name: "unknown representation", bytes: valid, dimensions: 2, representation: "dense_f16", normalization: NormalizationL2},
		{name: "unknown normalization", bytes: valid, dimensions: 2, representation: RepresentationDenseF32, normalization: "unit-ish"},
		{name: "zero L2", bytes: EncodeDenseF32([]float32{0, 0}), dimensions: 2, representation: RepresentationDenseF32, normalization: NormalizationL2},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if err := ValidateEncodedVector(tc.bytes, tc.dimensions, tc.representation, tc.normalization); err == nil {
				t.Fatal("invalid encoded vector unexpectedly succeeded")
			}
		})
	}
}
