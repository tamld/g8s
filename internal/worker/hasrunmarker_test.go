package worker

import (
	"testing"
)

func TestHasRunMarkerLinux(t *testing.T) {
	target := "G8S_RUN_MARKER=123"
	targetBytes := []byte(target)
	cases := []struct {
		name string
		data string
		want bool
	}{
		{"exact match only", "G8S_RUN_MARKER=123", true},
		{"match at start", "G8S_RUN_MARKER=123\x00OTHER=abc", true},
		{"match in middle", "FOO=bar\x00G8S_RUN_MARKER=123\x00BAZ=qux", true},
		{"match at end", "FOO=bar\x00G8S_RUN_MARKER=123", true},
		{"prefix only no match", "FOO=bar\x00G8S_RUN_MARKER=1234\x00", false},
		{"suffix only no match", "XG8S_RUN_MARKER=123\x00", false},
		{"no match", "FOO=bar\x00", false},
		{"empty", "", false},
		{"nulls only", "\x00\x00", false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := hasRunMarkerLinuxOpt([]byte(c.data), targetBytes); got != c.want {
				t.Errorf("hasRunMarkerLinuxOpt(%q) = %v; want %v", c.data, got, c.want)
			}
		})
	}
}

func TestHasRunMarkerDarwin(t *testing.T) {
	target := "G8S_RUN_MARKER=123"
	targetBytes := []byte(target)
	cases := []struct {
		name string
		out  string
		want bool
	}{
		{"exact match", "G8S_RUN_MARKER=123", true},
		{"wrapped in spaces", " G8S_RUN_MARKER=123 ", true},
		{"wrapped in newlines", "\nG8S_RUN_MARKER=123\n", true},
		{"middle of ps output", "1234 mycmd G8S_RUN_MARKER=123 OTHER=abc", true},
		{"prefix only no match", "cmd G8S_RUN_MARKER=1234", false},
		{"suffix only no match", "cmd XG8S_RUN_MARKER=123", false},
		{"no match", "cmd G8S_OTHER_MARKER=123", false},
		{"empty", "", false},
		{"spaces only", "   ", false},
		{"tabs", "\tG8S_RUN_MARKER=123\t", true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := hasRunMarkerDarwinOpt([]byte(c.out), targetBytes); got != c.want {
				t.Errorf("hasRunMarkerDarwinOpt(%q) = %v; want %v", c.out, got, c.want)
			}
		})
	}
}
