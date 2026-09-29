package dispatch

import (
	"encoding/json"
	"os"
	"os/exec"
	"strings"
	"testing"
)

func TestResultProviderJSONMarshaling(t *testing.T) {
	tests := []struct {
		name         string
		res          Result
		wantProvider bool
		providerVal  string
	}{
		{
			name: "provider set is serialized",
			res: Result{
				OK:         true,
				Provider:   "codex",
				Model:      "codex-1",
				Role:       "collector",
				Permission: "read_only",
				AGYBin:     "/usr/local/bin/codex",
			},
			wantProvider: true,
			providerVal:  "codex",
		},
		{
			name: "empty provider is omitted",
			res: Result{
				OK:         true,
				Model:      "gemini-3.8-flash-high",
				Role:       "collector",
				Permission: "read_only",
				AGYBin:     "/usr/local/bin/agy",
			},
			wantProvider: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			raw, err := json.Marshal(tt.res)
			if err != nil {
				t.Fatalf("Marshal: %v", err)
			}
			str := string(raw)

			// agy_bin must always marshal
			if !strings.Contains(str, `"agy_bin"`) {
				t.Errorf("expected agy_bin in serialized json, got: %s", str)
			}

			var parsed map[string]any
			if err := json.Unmarshal(raw, &parsed); err != nil {
				t.Fatalf("Unmarshal: %v", err)
			}

			val, ok := parsed["provider"]
			if tt.wantProvider {
				if !ok {
					t.Errorf("expected provider key in JSON, got: %s", str)
				}
				if val != tt.providerVal {
					t.Errorf("got provider %v, want %v", val, tt.providerVal)
				}
			} else {
				if ok {
					t.Errorf("expected provider key to be omitted, got: %s", str)
				}
			}
		})
	}
}

func TestRunStampsProvider(t *testing.T) {
	tests := []struct {
		name         string
		optsProvider string
		wantProvider string
	}{
		{
			name:         "non-empty provider stamped",
			optsProvider: "codex",
			wantProvider: "codex",
		},
		{
			name:         "empty provider remains empty",
			optsProvider: "",
			wantProvider: "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Resolve to a real, fast-spawning executable that exists on
			// every CI platform (unlike /bin/echo) and passes identity
			// checks. The stubbed Runner below means it is never executed
			// by Run; a heavyweight override (the multi-MB test binary)
			// makes VerifyExecutableIdentity spawn it four times and adds
			// seconds of load that starves timing-sensitive sibling tests
			// on 2-core CI runners.
			override, err := exec.LookPath("go")
			if err != nil {
				override, err = os.Executable()
				if err != nil {
					t.Fatalf("resolve override binary: %v", err)
				}
			}
			opts := RunOptions{
				Prompt:   "hello",
				Provider: tt.optsProvider,
				Runner: func(command []string) (ExecResult, error) {
					return ExecResult{ReturnCode: 0}, nil
				},
				BinaryOverride: override,
			}
			res, err := Run(opts)
			if err != nil {
				t.Fatalf("Run: %v", err)
			}
			if res.Provider != tt.wantProvider {
				t.Errorf("res.Provider = %q, want %q", res.Provider, tt.wantProvider)
			}
		})
	}
}
