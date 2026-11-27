package mcp

// Authority Sink Gate registry reality test — issue #977 PR-2 (item 5).
//
// The registry (.g8s/authority-sinks.yml) is the declarative audit trail for
// the privileged sinks in the Context Authority Firewall source-to-sink map
// (docs/security/context-authority-firewall-threat-model.md §2). This test
// makes drift impossible to merge silently: the registry must MATCH REALITY.
//
// Mechanical checks per sink:
//   - status is one of gated | partial | unguarded;
//   - ref names a file that exists at the repo root;
//   - gate_symbol names a function/method actually declared in ref (go/parser
//     AST check — no string matching against source text);
//   - a sink bound to an MCP tool names a tool that is registered on the
//     server (white-box s.tools check);
//   - the two PR-2-enforced minting sinks cite the shared workerTierGuard
//     symbol, tying the registry to the enforcement code.
//
// PR-3 consumes this registry for deterministic sink decisions; until then it
// is the audit backbone that keeps the threat-model map honest.

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"testing"

	"go.yaml.in/yaml/v3"
)

const authoritySinksRegistry = ".g8s/authority-sinks.yml"

type authoritySink struct {
	ID             string   `yaml:"id"`
	Surface        string   `yaml:"surface"`
	Tool           string   `yaml:"tool"`
	Ref            string   `yaml:"ref"`
	GateSymbol     string   `yaml:"gate_symbol"`
	Gate           string   `yaml:"gate"`
	Status         string   `yaml:"status"`
	Defects        []string `yaml:"defects"`
	ThreatModelRef string   `yaml:"threat_model_ref"`
}

type authoritySinksRegistryFile struct {
	SchemaVersion string          `yaml:"schema_version"`
	Sinks         []authoritySink `yaml:"sinks"`
}

func loadAuthoritySinksRegistry(t *testing.T) authoritySinksRegistryFile {
	t.Helper()
	repoRoot := filepath.Clean(filepath.Join("..", ".."))
	raw, err := os.ReadFile(filepath.Join(repoRoot, authoritySinksRegistry))
	if err != nil {
		t.Fatalf("read %s: %v", authoritySinksRegistry, err)
	}
	var reg authoritySinksRegistryFile
	if err := yaml.Unmarshal(raw, &reg); err != nil {
		t.Fatalf("parse %s: %v", authoritySinksRegistry, err)
	}
	return reg
}

// fileDeclaresSymbol reports whether the gate symbol is declared in the Go
// package the ref file belongs to (go/parser AST check — no string matching
// against source text).
//
// A sink's ref points at the file where the gated TOOL lives; its gate_symbol
// may be a package-level helper declared in a sibling file of the same
// package (workerTierGuard lives in tools_ext.go and guards the
// receipt/charter/mesh sinks). The symbol must be declared in ref itself or
// in a non-test sibling .go file of ref's package directory.
func fileDeclaresSymbol(t *testing.T, ref, symbol string) bool {
	t.Helper()
	repoRoot := filepath.Clean(filepath.Join("..", ".."))
	path := filepath.Join(repoRoot, ref)
	if goFileDeclaresFunc(t, path, symbol) {
		return true
	}
	siblings, err := filepath.Glob(filepath.Join(filepath.Dir(path), "*.go"))
	if err != nil {
		t.Fatalf("glob package dir of %s: %v", ref, err)
	}
	for _, sibling := range siblings {
		if strings.HasSuffix(sibling, "_test.go") || filepath.Base(sibling) == filepath.Base(path) {
			continue
		}
		if goFileDeclaresFunc(t, sibling, symbol) {
			return true
		}
	}
	return false
}

func goFileDeclaresFunc(t *testing.T, path, symbol string) bool {
	t.Helper()
	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, path, nil, 0)
	if err != nil {
		t.Fatalf("parse %s: %v", path, err)
	}
	for _, decl := range file.Decls {
		if fn, ok := decl.(*ast.FuncDecl); ok && fn.Name.Name == symbol {
			return true
		}
	}
	return false
}

func TestAuthoritySinksRegistry_MatchesReality(t *testing.T) {
	reg := loadAuthoritySinksRegistry(t)

	if reg.SchemaVersion != "authority-sinks.v1" {
		t.Fatalf("schema_version = %q, want authority-sinks.v1", reg.SchemaVersion)
	}
	if len(reg.Sinks) < 10 {
		t.Fatalf("registry enumerates %d sinks, want >= 10 (the threat-model §2 privileged sink set)", len(reg.Sinks))
	}

	seen := make(map[string]bool, len(reg.Sinks))
	s, _, _, _ := newTestServer(t)

	for _, sink := range reg.Sinks {
		if sink.ID == "" {
			t.Fatal("registry sink with empty id")
		}
		if seen[sink.ID] {
			t.Fatalf("duplicate sink id %q", sink.ID)
		}
		seen[sink.ID] = true

		switch sink.Status {
		case "gated", "partial", "unguarded":
		default:
			t.Errorf("sink %s: status %q must be gated|partial|unguarded", sink.ID, sink.Status)
		}
		if sink.Gate == "" {
			t.Errorf("sink %s: missing gate description", sink.ID)
		}
		if sink.Ref == "" {
			t.Errorf("sink %s: missing ref", sink.ID)
			continue
		}
		repoRoot := filepath.Clean(filepath.Join("..", ".."))
		if _, err := os.Stat(filepath.Join(repoRoot, sink.Ref)); err != nil {
			t.Errorf("sink %s: ref %s does not exist", sink.ID, sink.Ref)
			continue
		}
		if sink.GateSymbol == "" {
			t.Errorf("sink %s: missing gate_symbol", sink.ID)
			continue
		}
		if !fileDeclaresSymbol(t, sink.Ref, sink.GateSymbol) {
			t.Errorf("sink %s: gate symbol %q is not declared in %s (registry drifted from code)", sink.ID, sink.GateSymbol, sink.Ref)
		}
		if sink.Tool != "" {
			if _, ok := s.tools[sink.Tool]; !ok {
				t.Errorf("sink %s: tool %q is not registered on the MCP server", sink.ID, sink.Tool)
			}
		}
	}

	// The two PR-2-enforced minting sinks must cite the shared guard: the
	// registry is the audit trail OF the enforcement, not prose next to it.
	for _, want := range []string{"receipt_mint_mcp", "charter_issue_mcp"} {
		if sink := findSink(reg.Sinks, want); sink == nil {
			t.Fatalf("registry missing the PR-2-enforced sink %q", want)
		} else if sink.Status != "gated" || sink.GateSymbol != "workerTierGuard" {
			t.Fatalf("sink %s must be gated via workerTierGuard, got status=%s gate_symbol=%s", want, sink.Status, sink.GateSymbol)
		}
	}

	// No unguarded sinks may remain post-PR-2 (the V8 pair is closed; any new
	// unguarded sink must be a conscious, reviewed registry edit).
	for _, sink := range reg.Sinks {
		if sink.Status == "unguarded" {
			t.Errorf("sink %s is registered as unguarded — post-PR-2 the registry must not carry unguarded privileged sinks", sink.ID)
		}
	}
}

func findSink(sinks []authoritySink, id string) *authoritySink {
	for i := range sinks {
		if sinks[i].ID == id {
			return &sinks[i]
		}
	}
	return nil
}

// TestAuthoritySinksRegistry_CoversThreatModelSinkTable — the registry must
// enumerate every privileged sink row of the threat-model §2 map (by id), so
// a sink added to the map without a registry entry fails here.
func TestAuthoritySinksRegistry_CoversThreatModelSinkTable(t *testing.T) {
	reg := loadAuthoritySinksRegistry(t)
	wantIDs := []string{
		"receipt_mint_cli",
		"receipt_mint_mcp",
		"charter_issue_mcp",
		"charter_issue_deep",
		"mesh_send_mcp",
		"tcb_mutation_deliver",
		"trust_boundary_file_writes",
		"worker_capability_grant",
		"memory_promote_durable",
		"lane_s_escalation",
	}
	gotIDs := make([]string, 0, len(reg.Sinks))
	for _, sink := range reg.Sinks {
		gotIDs = append(gotIDs, sink.ID)
	}
	sort.Strings(gotIDs)
	want := append([]string(nil), wantIDs...)
	sort.Strings(want)
	if !reflect.DeepEqual(gotIDs, want) {
		t.Fatalf("registry sink ids = %v, want exactly %v (extend both the registry and this test together)", gotIDs, want)
	}
}
