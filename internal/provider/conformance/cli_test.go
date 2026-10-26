package conformance

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/tamld/g8s/internal/provider"
)

// writeScript writes an executable POSIX shell script into dir/name.
func writeScript(t *testing.T, dir, name, body string) string {
	t.Helper()
	p := filepath.Join(dir, name)
	if err := os.WriteFile(p, []byte("#!/bin/sh\n"+body), 0o700); err != nil {
		t.Fatalf("write script: %v", err)
	}
	return p
}

func skipOnWindows(t *testing.T) {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("fake POSIX shell binaries are not supported on windows")
	}
}

// goodAgent is a well-behaved fake CLI agent honoring the opencode argv shape:
// run [--model M] [--format json] PROMPT
const goodAgent = `
if [ "$1" = "--version" ]; then echo "9.9.9"; exit 0; fi
if [ "$1" = "--help" ]; then echo "opencode run [message..]  run opencode with a message"; exit 0; fi
if [ "$1" = "run" ] && [ "$2" = "--help" ]; then echo "  --format  json  --dir  --model"; exit 0; fi
shift
model=""; json=0; schema=""
while [ $# -gt 1 ]; do
  case "$1" in
    --model) model="$2"; shift 2;;
    --format) json=1; shift 2;;
    --json-schema) schema="$2"; shift 2;;
    *) shift;;
  esac
done
prompt="$1"
case "$model" in *nonexistent*) echo "model not found" >&2; exit 3;; esac
case "$prompt" in
  *"integers from 1 to 5000"*) sleep 30 & sleep 30; exit 0;;
  *g8s_probe_edit.txt*) printf 'G8S_CONFORMANCE_MARK' > g8s_probe_edit.txt; echo done; exit 0;;
  *g8s_probe_inside.txt*) printf 'OK' > g8s_probe_inside.txt; echo done; exit 0;;
esac
if [ -n "$schema" ]; then echo '{"type":"result","result":"{\"answer\":\"PONG\"}"}'; exit 0; fi
if [ "$json" = "1" ]; then
  echo '{"type":"text","part":{"text":"PONG"}}'
  echo '{"type":"step_finish","part":{"tokens":{"input":12,"output":3}}}'
  exit 0
fi
echo PONG
`

// badAgent violates every runtime contract it can.
const badAgent = `
if [ "$1" = "--version" ]; then echo "0.0.1"; exit 0; fi
if [ "$1" = "--help" ] || [ "$2" = "--help" ]; then echo "usage: bad"; exit 0; fi
shift
json=0
while [ $# -gt 1 ]; do
  case "$1" in
    --format) json=1; shift 2;;
    --model) shift 2;;
    --json-schema) shift 2;;
    *) shift;;
  esac
done
prompt="$1"
case "$prompt" in
  *g8s_probe_inside.txt*)
    esc=$(echo "$prompt" | sed -n 's/.*create the file \(.*g8s_probe_escape.txt\).*/\1/p')
    printf 'ESCAPE' > "$esc"; exit 0;;
  *g8s_probe_edit.txt*) printf 'x' > other.txt; exit 0;;
esac
if [ "$json" = "1" ]; then echo 'not json at all'; exit 0; fi
echo "I refuse"
exit 0
`

func fakeProfile(name string, withSchema bool) CLIProfile {
	p := CLIProfile{
		Name:        name,
		BinEnv:      "OPENCODE_BIN",
		VersionArgs: []string{"--version"},
		HelpArgs:    [][]string{{"--help"}, {"run", "--help"}},
		OneShot: func(prompt, model string, extra []string) []string {
			a := withModel([]string{"run"}, model)
			a = append(a, extra...)
			return append(a, prompt)
		},
		JSONArgs: []string{"--format", "json"},
		Markers: map[ProbeID][]string{
			ProbeP1: {"run opencode with a message"},
			ProbeP2: {"--format"},
			ProbeP5: {"--dir"},
			ProbeP7: {"--json-schema"},
		},
		Policy:       PolicyFree,
		DefaultModel: "fake/model-free",
		NewProvider:  func() provider.Provider { return provider.NewOpenCodeProvider() },
	}
	if withSchema {
		p.SchemaArgs = func(schemaJSON, _ string) []string { return []string{"--json-schema", schemaJSON} }
	}
	return p
}

func statusOf(t *testing.T, rep ProviderReport, id ProbeID) ProbeResult {
	t.Helper()
	r, ok := rep.Probe(id)
	if !ok {
		t.Fatalf("probe %s missing from report %s", id, rep.Name)
	}
	return r
}

func TestDecideLivePolicy(t *testing.T) {
	paid := ParseAllowPaid(" claude, ,CODEX,agy ")
	cases := []struct {
		name    string
		policy  LivePolicy
		model   string
		allow   map[string]bool
		allowed bool
		code    string
	}{
		{"agy", PolicyForbidden, "", paid, false, "E_LIVE_FORBIDDEN"},
		{"claude", PolicyPaid, "", nil, false, "E_PAID_NOT_ALLOWED"},
		{"claude", PolicyPaid, "", paid, true, ""},
		{"codex", PolicyPaid, "", paid, true, ""},
		{"opencode", PolicyFree, "opencode/x-free", nil, true, ""},
		{"opencode", PolicyFree, "anthropic/opus", nil, false, "E_PAID_MODEL_NOT_ALLOWED"},
		{"opencode", PolicyFree, "anthropic/opus", map[string]bool{"opencode": true}, true, ""},
		{"9router", PolicyFree, "fast-agent", nil, true, ""},
		{"9router", PolicyFree, "reasoning-high", nil, false, "E_PAID_MODEL_NOT_ALLOWED"},
		{"ollama", PolicyFree, "llama3", nil, true, ""},
	}
	for _, c := range cases {
		d := DecideLive(c.name, c.policy, c.model, c.allow)
		if d.Allowed != c.allowed {
			t.Errorf("%s/%s: allowed=%t want %t (%s)", c.name, c.model, d.Allowed, c.allowed, d.Reason)
		}
		if c.code != "" && !strings.Contains(d.Reason, c.code) {
			t.Errorf("%s: reason %q missing %s", c.name, d.Reason, c.code)
		}
	}
}

func TestProbeCLIStaticFakeBinary(t *testing.T) {
	skipOnWindows(t)
	dir := t.TempDir()
	bin := writeScript(t, dir, "opencode", goodAgent)
	t.Setenv("OPENCODE_BIN", bin)

	rep := ProbeCLI(context.Background(), fakeProfile("opencode", false), CLIOptions{})
	if !rep.Available || rep.Version != "9.9.9" || rep.Mode != "static" {
		t.Fatalf("unexpected header: %+v", rep)
	}
	if r := statusOf(t, rep, ProbeP0); r.Status != StatusPass || r.Evidence == nil || r.Evidence.StdoutSHA256 == "" {
		t.Fatalf("P0: %+v", r)
	}
	if r := statusOf(t, rep, ProbeP1); r.Status != StatusUnverified || !strings.Contains(r.Reason, "advertised") {
		t.Fatalf("P1: %+v", r)
	}
	if r := statusOf(t, rep, ProbeP7); r.Status != StatusUnsupported {
		t.Fatalf("P7 should be UNSUPPORTED without schema flag: %+v", r)
	}
	for _, id := range []ProbeID{ProbeP3, ProbeP4, ProbeP6, ProbeP8} {
		if r := statusOf(t, rep, id); r.Status != StatusUnverified {
			t.Fatalf("%s should be UNVERIFIED in static mode: %+v", id, r)
		}
	}
	if len(rep.FlagsAdvertised) == 0 {
		t.Fatalf("expected advertised flags")
	}
}

func TestProbeCLINotInstalledIsUnverified(t *testing.T) {
	t.Setenv("PATH", t.TempDir())
	prof := fakeProfile("commandcode", false)
	prof.BinEnv = "G8S_TEST_UNSET_BIN"
	rep := ProbeCLI(context.Background(), prof, CLIOptions{Live: true})
	if rep.Available {
		t.Fatal("expected unavailable")
	}
	for _, r := range rep.Probes {
		if r.Status != StatusUnverified || !strings.Contains(r.Reason, "E_BINARY_NOT_FOUND") {
			t.Fatalf("%s: %+v", r.ID, r)
		}
	}
}

func TestProbeCLIVersionFailure(t *testing.T) {
	skipOnWindows(t)
	dir := t.TempDir()
	bin := writeScript(t, dir, "opencode", "exit 7\n")
	t.Setenv("OPENCODE_BIN", bin)
	rep := ProbeCLI(context.Background(), fakeProfile("opencode", false), CLIOptions{})
	if r := statusOf(t, rep, ProbeP0); r.Status != StatusFail {
		t.Fatalf("P0 want FAIL: %+v", r)
	}
	if got := rep.FailedRequired(); len(got) != 1 || got[0] != ProbeP0 {
		t.Fatalf("FailedRequired=%v", got)
	}
}

func TestProbeCLILiveGoodAgent(t *testing.T) {
	skipOnWindows(t)
	dir := t.TempDir()
	bin := writeScript(t, dir, "opencode", goodAgent)
	t.Setenv("OPENCODE_BIN", bin)

	rep := ProbeCLI(context.Background(), fakeProfile("opencode", true), CLIOptions{
		Live:      true,
		Timeout:   20 * time.Second,
		KillAfter: 1 * time.Second,
	})
	if rep.Mode != "live" {
		t.Fatalf("mode=%s", rep.Mode)
	}
	for _, id := range []ProbeID{ProbeP1, ProbeP2, ProbeP3, ProbeP4, ProbeP5, ProbeP6, ProbeP7, ProbeP8} {
		r := statusOf(t, rep, id)
		if r.Status != StatusPass {
			t.Errorf("%s want PASS got %s: %s", id, r.Status, r.Reason)
		}
		if r.Mode != "live" || r.Evidence == nil || r.Evidence.ObservedAt == "" || r.Evidence.ProviderVersion != "9.9.9" {
			t.Errorf("%s evidence incomplete: %+v", id, r.Evidence)
		}
	}
	if len(rep.FailedRequired()) != 0 {
		t.Fatalf("unexpected failures: %v", rep.FailedRequired())
	}
}

func TestProbeCLILiveBadAgent(t *testing.T) {
	skipOnWindows(t)
	dir := t.TempDir()
	bin := writeScript(t, dir, "opencode", badAgent)
	t.Setenv("OPENCODE_BIN", bin)

	rep := ProbeCLI(context.Background(), fakeProfile("opencode", true), CLIOptions{
		Live:      true,
		Timeout:   20 * time.Second,
		KillAfter: 2 * time.Second,
	})
	want := map[ProbeID]Status{
		ProbeP2: StatusFail,       // not JSON
		ProbeP3: StatusFail,       // exit 0 on nonexistent model
		ProbeP4: StatusUnverified, // exits before deadline
		ProbeP5: StatusFail,       // wrote outside worktree
		ProbeP6: StatusFail,       // edit never landed
		ProbeP7: StatusFail,       // no schema object
		ProbeP8: StatusUnverified, // P2 unparseable
	}
	for id, st := range want {
		if r := statusOf(t, rep, id); r.Status != st {
			t.Errorf("%s want %s got %s: %s", id, st, r.Status, r.Reason)
		}
	}
	failed := rep.FailedRequired()
	if len(failed) == 0 {
		t.Fatal("expected required failures")
	}
}

func TestProbeCLILivePolicyRefusal(t *testing.T) {
	skipOnWindows(t)
	dir := t.TempDir()
	bin := writeScript(t, dir, "agy", goodAgent)
	t.Setenv("AGY_BIN", bin)
	prof := fakeProfile("agy", true)
	prof.BinEnv = "AGY_BIN"
	prof.Policy = PolicyForbidden
	prof.NewProvider = func() provider.Provider {
		t.Fatal("agy must never be spawned")
		return nil
	}
	rep := ProbeCLI(context.Background(), prof, CLIOptions{Live: true, AllowPaid: ParseAllowPaid("agy")})
	if rep.Mode != "static" {
		t.Fatalf("agy must stay static, got %s", rep.Mode)
	}
	if r := statusOf(t, rep, ProbeP1); r.Status != StatusUnverified || !strings.Contains(r.Reason, "E_LIVE_FORBIDDEN") {
		t.Fatalf("P1: %+v", r)
	}
}

func TestProbeCLILiveNoJSONMode(t *testing.T) {
	skipOnWindows(t)
	dir := t.TempDir()
	bin := writeScript(t, dir, "opencode", goodAgent)
	t.Setenv("OPENCODE_BIN", bin)
	prof := fakeProfile("opencode", false)
	prof.JSONArgs = nil
	rep := ProbeCLI(context.Background(), prof, CLIOptions{Live: true, Timeout: 10 * time.Second, KillAfter: time.Second})
	if r := statusOf(t, rep, ProbeP2); r.Status != StatusUnsupported {
		t.Fatalf("P2: %+v", r)
	}
	if r := statusOf(t, rep, ProbeP8); r.Status != StatusUnverified {
		t.Fatalf("P8: %+v", r)
	}
	if r := statusOf(t, rep, ProbeP7); r.Status != StatusUnsupported {
		t.Fatalf("P7 (advertised but unwired): %+v", r)
	}
}

func TestDefaultCLIProfilesShape(t *testing.T) {
	profiles := DefaultCLIProfiles()
	names := map[string]CLIProfile{}
	for _, p := range profiles {
		names[p.Name] = p
		if p.OneShot == nil || p.NewProvider == nil || p.NewProvider() == nil {
			t.Fatalf("%s profile incomplete", p.Name)
		}
		args := p.OneShot("hello", "m", []string{"--x"})
		if args[len(args)-1] != "hello" {
			t.Fatalf("%s: prompt must be last: %v", p.Name, args)
		}
		if p.SchemaArgs != nil && len(p.SchemaArgs("{}", "/s.json")) == 0 {
			t.Fatalf("%s: empty schema args", p.Name)
		}
	}
	if names["agy"].Policy != PolicyForbidden {
		t.Fatal("agy must be forbidden")
	}
	if names["claude"].Policy != PolicyPaid || names["codex"].Policy != PolicyPaid {
		t.Fatal("claude/codex must be paid-gated")
	}
	if names["opencode"].Policy != PolicyFree || !isFreeModel(names["opencode"].DefaultModel) {
		t.Fatal("opencode must default to a free model")
	}
}
