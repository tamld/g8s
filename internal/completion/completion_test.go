package completion

import (
	"strings"
	"testing"
)

func TestGenerateAllShellCompletions(t *testing.T) {
	for _, shell := range SupportedShells {
		script, err := Generate(shell)
		if err != nil {
			t.Fatalf("Generate(%q): %v", shell, err)
		}
		if len(script) == 0 {
			t.Fatalf("empty script for %s", shell)
		}

		for _, cmd := range []string{"submit", "doctor", "init", "config", "completion"} {
			if !strings.Contains(script, cmd) {
				t.Errorf("script for %s missing command %q", shell, cmd)
			}
		}
	}
}

func TestGenerateUnsupportedShell(t *testing.T) {
	_, err := Generate("powershell_unsupported")
	if err == nil {
		t.Fatalf("expected error on unsupported shell")
	}
}
