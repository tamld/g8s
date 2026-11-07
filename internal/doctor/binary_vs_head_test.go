package doctor

import (
	"strings"
	"testing"
)

func TestBinaryRevisionsMatch(t *testing.T) {
	cases := []struct {
		name, bin, head string
		want            bool
	}{
		{"identical full shas", "e75ed9abcaec4941c73158d6d074de37af9bf51f", "e75ed9abcaec4941c73158d6d074de37af9bf51f", true},
		{"short vs full", "e75ed9a", "e75ed9abcaec4941c73158d6d074de37af9bf51f", true},
		{"dirty suffix tolerated", "e75ed9abcaec4941c73158d6d074de37af9bf51f-dirty", "E75ED9ABCAEC4941C73158D6D074DE37AF9BF51F", true},
		{"differ", "e75ed9abcaec4941c73158d6d074de37af9bf51f", "0123456789abcdef0123456789abcdef01234567", false},
		{"too short to attest", "e75ed9", "e75ed9", false},
		{"empty", "", "e75ed9abcaec4941c73158d6d074de37af9bf51f", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := binaryRevisionsMatch(tc.bin, tc.head); got != tc.want {
				t.Fatalf("binaryRevisionsMatch(%q, %q) = %v, want %v", tc.bin, tc.head, got, tc.want)
			}
		})
	}
}

func TestCheckBinaryVsHeadFrom(t *testing.T) {
	head := "e75ed9abcaec4941c73158d6d074de37af9bf51f"
	if r := checkBinaryVsHeadFrom("", ""); !strings.Contains(r.Message, "not a git checkout") || r.Status != "OK" {
		t.Fatalf("no-repo case: %+v", r)
	}
	if r := checkBinaryVsHeadFrom("", head); r.Status != "WARN" || !strings.Contains(r.Message, "no VCS revision") {
		t.Fatalf("unattestable-binary-in-repo case: %+v", r)
	}
	if r := checkBinaryVsHeadFrom(head, head); r.Status != "OK" {
		t.Fatalf("aligned case: %+v", r)
	}
	r := checkBinaryVsHeadFrom("0123456789abcdef0123456789abcdef01234567", head)
	if r.Status != "WARN" || !strings.Contains(r.Message, "stale binary") {
		t.Fatalf("stale case: %+v", r)
	}
	if !strings.Contains(r.Details, "binary=0123456") || !strings.Contains(r.Details, "head=e75ed9a") {
		t.Fatalf("stale case details: %+v", r)
	}
}
