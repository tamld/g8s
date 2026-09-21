package conv

import (
	"testing"
)

func BenchmarkNormalizeHeading(b *testing.B) {
	for i := 0; i < b.N; i++ {
		NormalizeHeading("1.1 Architecture & Design!")
	}
}

func TestNormalizeHeading(t *testing.T) {
	cases := []struct {
		input    string
		expected string
	}{
		{"1. Architecture", "architecture"},
		{"step 1: Design", "design"},
		{"1.1 Implementation!", "implementation"},
		{"# Hello World", "hello world"},
	}

	for _, c := range cases {
		got := NormalizeHeading(c.input)
		if got != c.expected {
			t.Errorf("NormalizeHeading(%q) == %q, expected %q", c.input, got, c.expected)
		}
	}
}
