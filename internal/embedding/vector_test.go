package embedding

import (
	"bytes"
	"math"
	"reflect"
	"testing"
)

func TestDenseF32LittleEndianRoundTrip(t *testing.T) {
	t.Parallel()
	values := []float32{1, -2.5, 0.25}
	encoded := EncodeDenseF32(values)
	wantBytes := []byte{
		0x00, 0x00, 0x80, 0x3f,
		0x00, 0x00, 0x20, 0xc0,
		0x00, 0x00, 0x80, 0x3e,
	}
	if !bytes.Equal(encoded, wantBytes) {
		t.Fatalf("encoded = %x, want %x", encoded, wantBytes)
	}
	decoded, err := DecodeDenseF32(encoded, len(values))
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(decoded, values) {
		t.Fatalf("decoded = %v, want %v", decoded, values)
	}
	decoded[0] = 99
	if encoded[0] != 0 {
		t.Fatal("decoded vector aliases encoded storage")
	}
}

func TestDecodeDenseF32RejectsCorruptLengthAndDimensions(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		bytes      []byte
		dimensions int
	}{
		{bytes: []byte{0, 0, 0}, dimensions: 1},
		{bytes: []byte{0, 0, 0, 0}, dimensions: 2},
		{bytes: nil, dimensions: 0},
		{bytes: nil, dimensions: int(^uint(0)>>1)/2 + 1},
	} {
		if _, err := DecodeDenseF32(tc.bytes, tc.dimensions); err == nil {
			t.Fatalf("DecodeDenseF32(%d bytes, %d dimensions) unexpectedly succeeded", len(tc.bytes), tc.dimensions)
		}
	}
}

func TestValidateDenseF32RejectsNonFiniteAndInvalidL2Vectors(t *testing.T) {
	t.Parallel()
	for _, vector := range [][]float32{
		{float32(math.NaN()), 0},
		{float32(math.Inf(1)), 0},
		{0, 0},
		{1, 1},
		{1},
	} {
		if err := ValidateDenseF32(vector, 2, NormalizationL2); err == nil {
			t.Fatalf("invalid L2 vector unexpectedly succeeded: %v", vector)
		}
	}
	if err := ValidateDenseF32([]float32{0.6, 0.8}, 2, NormalizationL2); err != nil {
		t.Fatalf("unit L2 vector: %v", err)
	}
	if err := ValidateDenseF32([]float32{0, 0}, 2, NormalizationNone); err != nil {
		t.Fatalf("finite unnormalized vector: %v", err)
	}
}
