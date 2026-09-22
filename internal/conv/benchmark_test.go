package conv

import (
	"testing"
)

func BenchmarkNormalizeHeading(b *testing.B) {
	headings := []string{
		"1. Introduction to the system",
		"Step 2: Setup",
		"Conclusion & Trade-offs!",
		"Architecture (High-level)",
		"1.2.3 Deep dive...",
	}
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		for _, h := range headings {
			NormalizeHeading(h)
		}
	}
}
