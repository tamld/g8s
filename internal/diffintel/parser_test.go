package diffintel

import (
	"strings"
	"testing"
)

const sampleDiff = `diff --git a/internal/auth/token.go b/internal/auth/token.go
index 1111111..2222222 100644
--- a/internal/auth/token.go
+++ b/internal/auth/token.go
@@ -10,6 +10,8 @@ func ValidateToken(token string) bool {
 	if token == "" {
 		return false
 	}
+	// new check
+	cache[token] = true
 	return true
 }
diff --git a/go.sum b/go.sum
index 3333333..4444444 100644
--- a/go.sum
+++ b/go.sum
@@ -1,2 +1,3 @@
 github.com/foo/bar v1.0.0 h1:abc=
+github.com/foo/baz v1.0.0 h1:def=
diff --git a/README.md b/README.md
new file mode 100644
--- /dev/null
+++ b/README.md
@@ -0,0 +1,3 @@
+# Project
+Welcome
+Enjoy
diff --git a/logo.png b/logo.png
new file mode 100644
Binary files /dev/null and b/logo.png differ
`

func TestParseUnifiedDiff(t *testing.T) {
	files, err := ParseUnifiedDiff(sampleDiff)
	if err != nil {
		t.Fatalf("ParseUnifiedDiff failed: %v", err)
	}

	if len(files) != 4 {
		t.Fatalf("expected 4 files, got %d", len(files))
	}

	// 1. internal/auth/token.go
	f1 := files[0]
	if f1.NewPath != "internal/auth/token.go" {
		t.Errorf("f1 NewPath = %s, expected internal/auth/token.go", f1.NewPath)
	}
	if len(f1.Hunks) != 1 {
		t.Fatalf("f1 expected 1 hunk, got %d", len(f1.Hunks))
	}
	hunk1 := f1.Hunks[0]
	if hunk1.NewStart != 10 || hunk1.NewLines != 8 {
		t.Errorf("hunk1 coords = %d,%d, expected 10,8", hunk1.NewStart, hunk1.NewLines)
	}
	if hunk1.Header != "func ValidateToken(token string) bool {" {
		t.Errorf("hunk1 header = %s", hunk1.Header)
	}

	// Verify line numbering inside hunk
	var foundAdd bool
	for _, l := range hunk1.Lines {
		if l.Type == LineAdd && strings.Contains(l.Content, "cache[token]") {
			foundAdd = true
			if l.NewLine != 14 {
				t.Errorf("expected added line to be at line 14, got %d", l.NewLine)
			}
		}
	}
	if !foundAdd {
		t.Errorf("did not find added line in hunk1")
	}

	// 2. go.sum
	f2 := files[1]
	if f2.NewPath != "go.sum" {
		t.Errorf("f2 NewPath = %s, expected go.sum", f2.NewPath)
	}

	// 3. README.md
	f3 := files[2]
	if !f3.IsNew {
		t.Errorf("f3 should be marked IsNew")
	}

	// 4. logo.png
	f4 := files[3]
	if !f4.IsBinary {
		t.Errorf("f4 should be marked IsBinary")
	}
}

func TestPruneNoise(t *testing.T) {
	files, err := ParseUnifiedDiff(sampleDiff)
	if err != nil {
		t.Fatalf("ParseUnifiedDiff failed: %v", err)
	}

	pruned, noiseCount := PruneNoise(files)
	if noiseCount != 2 { // go.sum and logo.png
		t.Errorf("expected 2 noise files pruned, got %d", noiseCount)
	}
	if len(pruned) != 2 {
		t.Fatalf("expected 2 pruned files, got %d", len(pruned))
	}
	if pruned[0].NewPath != "internal/auth/token.go" || pruned[1].NewPath != "README.md" {
		t.Errorf("unexpected pruned files: %+v", pruned)
	}
}

func TestSliceChunksAndRisk(t *testing.T) {
	files, err := ParseUnifiedDiff(sampleDiff)
	if err != nil {
		t.Fatalf("ParseUnifiedDiff failed: %v", err)
	}
	pruned, _ := PruneNoise(files)

	chunks := SliceChunks(pruned, 1000)
	if len(chunks) != 2 {
		t.Fatalf("expected 2 chunks, got %d", len(chunks))
	}

	// Check chunk 1 (auth)
	c1 := chunks[0]
	if c1.RiskLevel != "HIGH" {
		t.Errorf("c1 risk = %s, expected HIGH", c1.RiskLevel)
	}
	if !strings.Contains(c1.FormattedBody, "L0014: + \tcache[token] = true") {
		t.Errorf("c1 formatted body missing line anchor: %s", c1.FormattedBody)
	}

	// Check chunk 2 (readme)
	c2 := chunks[1]
	if c2.RiskLevel != "LOW" {
		t.Errorf("c2 risk = %s, expected LOW", c2.RiskLevel)
	}
}
