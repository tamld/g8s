package dispatch

import "testing"

func BenchmarkHasSuffix(b *testing.B) {
	cases := []string{
		"path/to/some/executable.EXE",
		"another/path/without/suffix",
		"very/long/path/with/a/lot/of/characters/to/make/lowercasing/expensive.bat",
	}
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		for _, c := range cases {
			hasSuffix(c)
		}
	}
}
