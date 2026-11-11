package mcp

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"regexp"
	"sort"
	"strings"
	"testing"

	"github.com/tamld/g8s/internal/harness"
)

func extractBacktickTokens(s string, re *regexp.Regexp) []string {
	matches := re.FindAllStringSubmatch(s, -1)
	seen := make(map[string]struct{}, len(matches))
	for _, m := range matches {
		if len(m) > 1 {
			seen[m[1]] = struct{}{}
		}
	}
	out := make([]string, 0, len(seen))
	for k := range seen {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

func sliceBetween(t *testing.T, docName, text, startMarker, endMarker string) string {
	t.Helper()
	start := strings.Index(text, startMarker)
	if start < 0 {
		t.Fatalf("%s missing start marker %q", docName, startMarker)
	}
	rest := text[start:]
	if endMarker == "" {
		return rest
	}
	end := strings.Index(rest[len(startMarker):], endMarker)
	if end < 0 {
		t.Fatalf("%s missing end marker %q after %q", docName, endMarker, startMarker)
	}
	return rest[:len(startMarker)+end]
}

func TestMCPAndRolesSSoTParity(t *testing.T) {
	repoRoot := filepath.Clean(filepath.Join("..", ".."))

	s, _, _, _ := newTestServer(t)
	if len(s.tools) != 23 || len(s.list) != 23 {
		t.Fatalf("registered MCP tools = %d/%d, want 23/23", len(s.tools), len(s.list))
	}

	wantTools := []string{
		"g8s_blast_radius",
		"g8s_cancel_task",
		"g8s_charter_issue",
		"g8s_charter_verify",
		"g8s_context_pack",
		"g8s_coordinator_mesh",
		"g8s_dispatch",
		"g8s_edit",
		"g8s_get",
		"g8s_list_permissions",
		"g8s_list_roles",
		"g8s_list_tasks",
		"g8s_llm_call",
		"g8s_portfolio_status",
		"g8s_read",
		"g8s_receipt_issue",
		"g8s_run",
		"g8s_search",
		"g8s_select",
		"g8s_self_awareness",
		"g8s_skills_list",
		"g8s_skills_read",
		"g8s_submit",
	}
	gotTools := make([]string, 0, len(s.list))
	for _, desc := range s.list {
		gotTools = append(gotTools, desc.Name)
	}
	sort.Strings(gotTools)
	if !reflect.DeepEqual(gotTools, wantTools) {
		t.Fatalf("server tool set = %v, want %v", gotTools, wantTools)
	}

	_, rpcErr := callTool(t, s, "g8s_run", map[string]any{"prompt": "ping"})
	if rpcErr == nil || rpcErr.Code != codeInternal {
		t.Fatalf("g8s_run should fail closed with JSON-RPC %d stub error, got %+v", codeInternal, rpcErr)
	}

	manifestBytes, err := os.ReadFile(filepath.Join(repoRoot, "manifest.json"))
	if err != nil {
		t.Fatalf("read manifest.json: %v", err)
	}
	var manifest struct {
		Version string `json:"version"`
		Runtime struct {
			Protocols []string `json:"protocols"`
		} `json:"runtime"`
		Roles []struct {
			Name string `json:"name"`
		} `json:"roles"`
	}
	if err := json.Unmarshal(manifestBytes, &manifest); err != nil {
		t.Fatalf("parse manifest.json: %v", err)
	}

	initResp := s.handleLine(context.Background(), requestLine("initialize", map[string]any{}))
	if initResp.Error != nil {
		t.Fatalf("initialize error: %+v", initResp.Error)
	}
	var initResult struct {
		ServerInfo struct {
			Name    string `json:"name"`
			Version string `json:"version"`
		} `json:"serverInfo"`
	}
	if err := json.Unmarshal(initResp.Result, &initResult); err != nil {
		t.Fatalf("decode initialize result: %v", err)
	}
	if initResult.ServerInfo.Version != manifest.Version {
		t.Fatalf("MCP serverInfo.version = %q, want manifest.json version %q", initResult.ServerInfo.Version, manifest.Version)
	}

	for _, proto := range manifest.Runtime.Protocols {
		if strings.Contains(proto, "LSP") {
			t.Fatalf("manifest.json runtime.protocols contains unimplemented LSP claim: %q", proto)
		}
	}
	if strings.Contains(string(manifestBytes), "LSP") {
		t.Fatalf("manifest.json contains unimplemented LSP claim")
	}

	noLSPDocs := []string{
		"README.md",
		"README.vi.md",
		"spec/openspec/README.md",
		"docs/user-guide/mcp-tools.md",
	}
	for _, rel := range noLSPDocs {
		raw, err := os.ReadFile(filepath.Join(repoRoot, rel))
		if err != nil {
			t.Fatalf("read %s: %v", rel, err)
		}
		if strings.Contains(string(raw), "LSP") {
			t.Errorf("%s contains unimplemented LSP runtime claim", rel)
		}
	}

	toolTokenRe := regexp.MustCompile("`(g8s_[a-z0-9_]+)`")
	docSections := []struct {
		rel   string
		start string
		end   string
	}{
		{rel: "README.md", start: "#### Complete MCP Tool Inventory", end: "### Agent Skills"},
		{rel: "README.vi.md", start: "#### Danh mục 23 công cụ MCP", end: "### Agent Skills"},
		{rel: "docs/SRS.md", start: "* **FR-5 (Stdio MCP Server)**:", end: "* **FR-6"},
		{rel: "docs/user-guide/mcp-tools.md", start: "## Tool surface", end: "## Tool details"},
		{rel: "docs/user-guide/cli-reference.md", start: "### 26. `g8s mcp`", end: "### 27."},
	}
	staleCountClaims := []string{
		"22 Tools",
		"Danh mục 22 công cụ MCP",
		"all 21 MCP tools",
		"21 MCP tool",
		"Expose 19 tools:",
		"16 tools exposed",
		"twenty-two-tool",
	}
	for _, spec := range docSections {
		raw, err := os.ReadFile(filepath.Join(repoRoot, spec.rel))
		if err != nil {
			t.Fatalf("read %s: %v", spec.rel, err)
		}
		fullText := string(raw)
		for _, stale := range staleCountClaims {
			if strings.Contains(fullText, stale) {
				t.Errorf("%s contains stale MCP tool count phrase %q", spec.rel, stale)
			}
		}
		section := sliceBetween(t, spec.rel, fullText, spec.start, spec.end)
		docTools := extractBacktickTokens(section, toolTokenRe)
		if !reflect.DeepEqual(docTools, wantTools) {
			t.Errorf("%s MCP inventory mismatch:\n got: %v\nwant: %v", spec.rel, docTools, wantTools)
		}
	}

	codeRoles := harness.RoleNames()
	if len(codeRoles) != 10 {
		t.Fatalf("len(harness.Roles) = %d, want 10", len(codeRoles))
	}
	manifestRoles := make([]string, 0, len(manifest.Roles))
	for _, r := range manifest.Roles {
		manifestRoles = append(manifestRoles, r.Name)
	}
	sort.Strings(manifestRoles)
	if !reflect.DeepEqual(codeRoles, manifestRoles) {
		t.Fatalf("role mismatch: harness.Roles=%v vs manifest.json=%v", codeRoles, manifestRoles)
	}

	srsBytes, err := os.ReadFile(filepath.Join(repoRoot, "docs/SRS.md"))
	if err != nil {
		t.Fatalf("read docs/SRS.md: %v", err)
	}
	srsText := string(srsBytes)
	fr1Section := sliceBetween(t, "docs/SRS.md", srsText, "* **FR-1 (Role & Permission Harness)**:", "* Enforce 4 permission profiles:")
	if !strings.Contains(fr1Section, "Enforce 10 distinct worker roles:") {
		t.Fatalf("docs/SRS.md FR-1 missing 'Enforce 10 distinct worker roles:'")
	}
	roleTokenRe := regexp.MustCompile("`([a-z0-9-]+)`")
	srsRoles := extractBacktickTokens(fr1Section, roleTokenRe)
	if !reflect.DeepEqual(srsRoles, codeRoles) {
		t.Fatalf("docs/SRS.md FR-1 role set = %v, want %v", srsRoles, codeRoles)
	}
}
