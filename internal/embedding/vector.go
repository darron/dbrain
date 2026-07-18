package embedding

import (
	"encoding/binary"
	"fmt"
	"math"
)

const (
	RepresentationDenseF32 = "dense_f32"
	NormalizationL2        = "l2"
	NormalizationNone      = "none"
)

func EncodeDenseF32(vector []float32) []byte {
	encoded := make([]byte, len(vector)*4)
	for i, value := range vector {
		binary.LittleEndian.PutUint32(encoded[i*4:], math.Float32bits(value))
	}
	return encoded
}

func DecodeDenseF32(encoded []byte, dimensions int) ([]float32, error) {
	if dimensions <= 0 {
		return nil, fmt.Errorf("embedding dimensions must be positive")
	}
	if dimensions > int(^uint(0)>>1)/4 {
		return nil, fmt.Errorf("embedding dimensions %d exceed dense_f32 capacity", dimensions)
	}
	want := dimensions * 4
	if len(encoded) != want {
		return nil, fmt.Errorf("dense_f32 byte length %d does not match dimensions %d (want %d)", len(encoded), dimensions, want)
	}
	vector := make([]float32, dimensions)
	for i := range vector {
		vector[i] = math.Float32frombits(binary.LittleEndian.Uint32(encoded[i*4:]))
	}
	return vector, nil
}
