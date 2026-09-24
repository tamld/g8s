package memory

import (
	"encoding/binary"
	"math"
)

// CosineSimilarity computes the cosine similarity between two float32 vectors.
// Returns a value between -1.0 and 1.0. Returns 0.0 if either vector has zero magnitude.
func CosineSimilarity(a, b []float32) (float64, error) {
	if len(a) == 0 || len(b) == 0 {
		return 0, ErrEmptyVector
	}
	if len(a) != len(b) {
		return 0, ErrDimensionMismatch
	}

	var dot, normA, normB float64
	for i := 0; i < len(a); i++ {
		ai := float64(a[i])
		bi := float64(b[i])
		dot += ai * bi
		normA += ai * ai
		normB += bi * bi
	}

	if normA == 0 || normB == 0 {
		return 0, nil
	}

	sim := dot / (math.Sqrt(normA) * math.Sqrt(normB))

	// Numerical clamp to [-1.0, 1.0] to handle float precision drift
	if sim > 1.0 {
		sim = 1.0
	} else if sim < -1.0 {
		sim = -1.0
	}

	return sim, nil
}

// EuclideanDistance computes the L2 distance between two float32 vectors.
func EuclideanDistance(a, b []float32) (float64, error) {
	if len(a) == 0 || len(b) == 0 {
		return 0, ErrEmptyVector
	}
	if len(a) != len(b) {
		return 0, ErrDimensionMismatch
	}

	var sumSq float64
	for i := 0; i < len(a); i++ {
		diff := float64(a[i]) - float64(b[i])
		sumSq += diff * diff
	}

	return math.Sqrt(sumSq), nil
}

// NormalizeVector produces a unit-length vector (L2 norm = 1.0).
func NormalizeVector(v []float32) ([]float32, error) {
	if len(v) == 0 {
		return nil, ErrEmptyVector
	}

	var normSq float64
	for _, x := range v {
		normSq += float64(x) * float64(x)
	}

	norm := math.Sqrt(normSq)
	if norm == 0 {
		res := make([]float32, len(v))
		copy(res, v)
		return res, nil
	}

	unit := make([]float32, len(v))
	invNorm := float32(1.0 / norm)
	for i, x := range v {
		unit[i] = x * invNorm
	}

	return unit, nil
}

// EncodeVectorBlob serializes a slice of float32 values into an IEEE 754 Little-Endian byte slice.
func EncodeVectorBlob(v []float32) []byte {
	buf := make([]byte, len(v)*4)
	for i, f := range v {
		binary.LittleEndian.PutUint32(buf[i*4:(i+1)*4], math.Float32bits(f))
	}
	return buf
}

// DecodeVectorBlob deserializes an IEEE 754 Little-Endian byte slice back into a slice of float32 values.
func DecodeVectorBlob(b []byte) ([]float32, error) {
	if len(b)%4 != 0 {
		return nil, ErrInvalidInput
	}

	dims := len(b) / 4
	v := make([]float32, dims)
	for i := 0; i < dims; i++ {
		bits := binary.LittleEndian.Uint32(b[i*4 : (i+1)*4])
		v[i] = math.Float32frombits(bits)
	}
	return v, nil
}
