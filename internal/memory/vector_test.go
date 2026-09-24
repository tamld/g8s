package memory

import (
	"errors"
	"math"
	"testing"
)

func TestCosineSimilarity(t *testing.T) {
	tests := []struct {
		name      string
		a         []float32
		b         []float32
		want      float64
		wantErr   error
		tolerance float64
	}{
		{
			name:      "identical vectors",
			a:         []float32{1.0, 2.0, 3.0},
			b:         []float32{1.0, 2.0, 3.0},
			want:      1.0,
			tolerance: 1e-6,
		},
		{
			name:      "scaled collinear vectors",
			a:         []float32{1.0, 2.0, 3.0},
			b:         []float32{2.0, 4.0, 6.0},
			want:      1.0,
			tolerance: 1e-6,
		},
		{
			name:      "orthogonal vectors",
			a:         []float32{1.0, 0.0},
			b:         []float32{0.0, 1.0},
			want:      0.0,
			tolerance: 1e-6,
		},
		{
			name:      "opposite vectors",
			a:         []float32{1.0, 0.0},
			b:         []float32{-1.0, 0.0},
			want:      -1.0,
			tolerance: 1e-6,
		},
		{
			name:      "zero magnitude vector",
			a:         []float32{0.0, 0.0},
			b:         []float32{1.0, 1.0},
			want:      0.0,
			tolerance: 1e-6,
		},
		{
			name:    "empty vector a",
			a:       []float32{},
			b:       []float32{1.0},
			wantErr: ErrEmptyVector,
		},
		{
			name:    "empty vector b",
			a:       []float32{1.0},
			b:       nil,
			wantErr: ErrEmptyVector,
		},
		{
			name:    "dimension mismatch",
			a:       []float32{1.0, 2.0},
			b:       []float32{1.0, 2.0, 3.0},
			wantErr: ErrDimensionMismatch,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := CosineSimilarity(tt.a, tt.b)
			if tt.wantErr != nil {
				if !errors.Is(err, tt.wantErr) {
					t.Fatalf("expected error %v, got %v", tt.wantErr, err)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if math.Abs(got-tt.want) > tt.tolerance {
				t.Errorf("CosineSimilarity() = %v, want %v (diff %v)", got, tt.want, math.Abs(got-tt.want))
			}
		})
	}
}

func TestEuclideanDistance(t *testing.T) {
	tests := []struct {
		name      string
		a         []float32
		b         []float32
		want      float64
		wantErr   error
		tolerance float64
	}{
		{
			name:      "identical vectors",
			a:         []float32{1.0, 2.0, 3.0},
			b:         []float32{1.0, 2.0, 3.0},
			want:      0.0,
			tolerance: 1e-6,
		},
		{
			name:      "3-4-5 triangle",
			a:         []float32{0.0, 3.0},
			b:         []float32{4.0, 0.0},
			want:      5.0,
			tolerance: 1e-6,
		},
		{
			name:    "empty vector",
			a:       nil,
			b:       []float32{1.0},
			wantErr: ErrEmptyVector,
		},
		{
			name:    "dimension mismatch",
			a:       []float32{1.0},
			b:       []float32{1.0, 2.0},
			wantErr: ErrDimensionMismatch,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := EuclideanDistance(tt.a, tt.b)
			if tt.wantErr != nil {
				if !errors.Is(err, tt.wantErr) {
					t.Fatalf("expected error %v, got %v", tt.wantErr, err)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if math.Abs(got-tt.want) > tt.tolerance {
				t.Errorf("EuclideanDistance() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestNormalizeVector(t *testing.T) {
	t.Run("valid vector 3-4", func(t *testing.T) {
		v := []float32{3.0, 4.0}
		norm, err := NormalizeVector(v)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if math.Abs(float64(norm[0])-0.6) > 1e-6 || math.Abs(float64(norm[1])-0.8) > 1e-6 {
			t.Errorf("NormalizeVector() = %v, want [0.6, 0.8]", norm)
		}
	})

	t.Run("zero vector returns copy", func(t *testing.T) {
		v := []float32{0.0, 0.0}
		norm, err := NormalizeVector(v)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if norm[0] != 0.0 || norm[1] != 0.0 {
			t.Errorf("NormalizeVector() = %v, want [0.0, 0.0]", norm)
		}
	})

	t.Run("empty vector returns error", func(t *testing.T) {
		_, err := NormalizeVector(nil)
		if !errors.Is(err, ErrEmptyVector) {
			t.Fatalf("expected ErrEmptyVector, got %v", err)
		}
	})
}

func TestEncodeDecodeVectorBlob(t *testing.T) {
	original := []float32{0.0, -1.25, 3.141592, 42.0, -999.5}
	blob := EncodeVectorBlob(original)

	if len(blob) != len(original)*4 {
		t.Fatalf("expected blob length %d, got %d", len(original)*4, len(blob))
	}

	decoded, err := DecodeVectorBlob(blob)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(decoded) != len(original) {
		t.Fatalf("expected decoded length %d, got %d", len(original), len(decoded))
	}

	for i := range original {
		if decoded[i] != original[i] {
			t.Errorf("index %d: got %v, want %v", i, decoded[i], original[i])
		}
	}

	// Invalid blob length
	invalidBlob := append(blob, 0x01, 0x02) // 2 extra bytes
	_, err = DecodeVectorBlob(invalidBlob)
	if !errors.Is(err, ErrInvalidInput) {
		t.Fatalf("expected ErrInvalidInput for invalid blob size, got %v", err)
	}
}

func BenchmarkCosineSimilarity_1536Dims(b *testing.B) {
	dims := 1536
	vecA := make([]float32, dims)
	vecB := make([]float32, dims)
	for i := 0; i < dims; i++ {
		vecA[i] = float32(i % 10)
		vecB[i] = float32((i + 3) % 10)
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, _ = CosineSimilarity(vecA, vecB)
	}
}
