package embedding

import (
	"fmt"
	"math"
)

const l2NormalizationTolerance = 1e-4

func ValidateDenseF32(vector []float32, dimensions int, normalization string) error {
	if dimensions <= 0 {
		return fmt.Errorf("embedding dimensions must be positive")
	}
	if len(vector) != dimensions {
		return fmt.Errorf("embedding vector dimensions %d do not match declared dimensions %d", len(vector), dimensions)
	}
	var squaredNorm float64
	for i, value := range vector {
		asFloat64 := float64(value)
		if math.IsNaN(asFloat64) || math.IsInf(asFloat64, 0) {
			return fmt.Errorf("embedding vector value %d is not finite", i)
		}
		squaredNorm += asFloat64 * asFloat64
	}
	switch normalization {
	case NormalizationNone:
		return nil
	case NormalizationL2:
		if squaredNorm == 0 {
			return fmt.Errorf("embedding L2 vector has zero norm")
		}
		if math.Abs(math.Sqrt(squaredNorm)-1) > l2NormalizationTolerance {
			return fmt.Errorf("embedding L2 vector norm %.9g is not unit length", math.Sqrt(squaredNorm))
		}
		return nil
	default:
		return fmt.Errorf("unsupported embedding normalization %q", normalization)
	}
}

func ValidateEncodedVector(encoded []byte, dimensions int, representation, normalization string) error {
	if representation != RepresentationDenseF32 {
		return fmt.Errorf("unsupported embedding representation %q", representation)
	}
	vector, err := DecodeDenseF32(encoded, dimensions)
	if err != nil {
		return err
	}
	return ValidateDenseF32(vector, dimensions, normalization)
}
