package harness

import (
	"slices"
	"strings"
	"testing"
)

func TestResolveCapabilityHydrationEnforcesPermissionAndRoleBoundaries(t *testing.T) {
	// 1. Read-only scout role must resolve to S3-A and exclude g8s_edit
	ro := ResolveCapabilityHydration("scout", "read_only")
	if ro.Role != "scout" {
		t.Errorf("ro.Role = %q, want %q", ro.Role, "scout")
	}
	if ro.Stratum != "S3-A" {
		t.Errorf("ro.Stratum = %q, want %q", ro.Stratum, "S3-A")
	}
	if slices.Contains(ro.AllowedMCPTools, "g8s_edit") {
		t.Errorf("read_only AllowedMCPTools must not include g8s_edit: %v", ro.AllowedMCPTools)
	}
	if !slices.Contains(ro.RecommendedSkills, "ck:scout") {
		t.Errorf("expected ck:scout in RecommendedSkills, got %v", ro.RecommendedSkills)
	}

	// 2. Workspace-write implementer role must resolve to S3-M and include g8s_edit
	rw := ResolveCapabilityHydration("implementer", "workspace_write")
	if rw.Role != "implementer" {
		t.Errorf("rw.Role = %q, want %q", rw.Role, "implementer")
	}
	if rw.Stratum != "S3-M" {
		t.Errorf("rw.Stratum = %q, want %q", rw.Stratum, "S3-M")
	}
	if !slices.Contains(rw.AllowedMCPTools, "g8s_edit") {
		t.Errorf("workspace_write AllowedMCPTools must include g8s_edit: %v", rw.AllowedMCPTools)
	}
	if !slices.Contains(rw.RecommendedSkills, "g8s-red-prove") {
		t.Errorf("expected g8s-red-prove in RecommendedSkills, got %v", rw.RecommendedSkills)
	}

	// 3. EnvVars formatting
	env := rw.EnvVars()
	joined := strings.Join(env, "\n")
	for _, want := range []string{
		"G8S_ROLE=implementer",
		"G8S_STRATUM=S3-M",
		"G8S_RECOMMENDED_SKILLS=g8s-red-prove,ck:cook,code-simplification",
		"G8S_MCP_TOOLS_ALLOWED=g8s_search,g8s_read,g8s_edit,g8s_select,g8s_status,g8s_get_task,g8s_doctor",
	} {
		if !strings.Contains(joined, want) {
			t.Errorf("missing %q in EnvVars:\n%s", want, joined)
		}
	}
}
