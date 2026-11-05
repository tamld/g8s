package testenv

import (
	"os"
	"os/exec"
	"strings"
	"testing"
)

func TestScrubAmbientDelegationEnv(t *testing.T) {
	for _, key := range AmbientDelegationEnvVars() {
		t.Setenv(key, "hostile")
	}
	t.Setenv("G8S_STATE_DIR", "keep-state-dir")
	t.Setenv("G8S_TESTENV_UNRELATED", "keep-unrelated")
	for range 2 {
		if err := ScrubAmbientDelegationEnv(); err != nil {
			t.Fatal(err)
		}
		for _, key := range AmbientDelegationEnvVars() {
			if _, present := os.LookupEnv(key); present {
				t.Errorf("%s remains set", key)
			}
		}
	}
	if os.Getenv("G8S_STATE_DIR") != "keep-state-dir" || os.Getenv("G8S_TESTENV_UNRELATED") != "keep-unrelated" {
		t.Fatal("scrubbing changed unrelated environment")
	}
}

func TestProductionPackagesDoNotImportTestenv(t *testing.T) {
	// Inspect only production imports; test imports intentionally use this package.
	cmd := exec.Command("go", "list", "-f", `{{.ImportPath}} {{join .Imports " "}}`, "../../...")
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("list production imports: %v\n%s", err, out)
	}
	const testenvPath = "github.com/tamld/g8s/internal/testenv"
	for line := range strings.SplitSeq(strings.TrimSpace(string(out)), "\n") {
		fields := strings.Fields(line)
		for _, imported := range fields[1:] {
			if imported == testenvPath {
				t.Errorf("production package %s imports test-only %s", fields[0], testenvPath)
			}
		}
	}
}
