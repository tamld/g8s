package conv

import (
	"testing"
)

func BenchmarkNormalizeHeading(b *testing.B) {
	for i := 0; i < b.N; i++ {
		NormalizeHeading("1.1 Architecture & Design!")
	}
}
