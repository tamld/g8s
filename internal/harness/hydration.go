package harness

import (
	"strings"
)

// CapabilityHydration describes the capability-scoped role, stratum, recommended
// shared skills, and allowed MCP tools inherited by F1 workers and F2 subagents
// within their zero-trust sandbox and receipt boundary.
type CapabilityHydration struct {
	Role              string   `json:"role"`
	Stratum           string   `json:"stratum"`
	Permission        string   `json:"permission"`
	RecommendedSkills []string `json:"recommended_skills"`
	AllowedMCPTools   []string `json:"allowed_mcp_tools"`
}

var (
	readOnlyMCPTools = []string{
		"g8s_search",
		"g8s_read",
		"g8s_select",
		"g8s_status",
		"g8s_get_task",
		"g8s_doctor",
	}
	workspaceWriteMCPTools = []string{
		"g8s_search",
		"g8s_read",
		"g8s_edit",
		"g8s_select",
		"g8s_status",
		"g8s_get_task",
		"g8s_doctor",
	}
)

// ResolveCapabilityHydration resolves the canonical role, orthogonal control
// stratum (S3-A vs S3-M), recommended shared skills, and permission-scoped MCP
// tools for an F1/F2 worker execution.
func ResolveCapabilityHydration(roleName, permission string) CapabilityHydration {
	perm := strings.TrimSpace(permission)
	if perm == "" {
		perm = "read_only"
	}

	resolvedRoleName := strings.TrimSpace(roleName)
	role, err := GetRole(resolvedRoleName)
	if err != nil {
		if perm == "workspace_write" || perm == "full_access" {
			role = Roles["implementer"]
		} else {
			role = Roles["collector"]
		}
	}

	stratum := role.Stratum
	if stratum == "" {
		if perm == "workspace_write" || perm == "full_access" {
			stratum = "S3-M"
		} else {
			stratum = "S3-A"
		}
	}
	if perm == "read_only" || perm == "supplied_text" {
		stratum = "S3-A"
	}

	var mcpTools []string
	if perm == "workspace_write" || perm == "full_access" {
		mcpTools = append(mcpTools, workspaceWriteMCPTools...)
	} else {
		mcpTools = append(mcpTools, readOnlyMCPTools...)
	}

	skills := make([]string, len(role.RecommendedSkills))
	copy(skills, role.RecommendedSkills)

	return CapabilityHydration{
		Role:              role.Name,
		Stratum:           stratum,
		Permission:        perm,
		RecommendedSkills: skills,
		AllowedMCPTools:   mcpTools,
	}
}

// EnvVars formats CapabilityHydration as deterministic environment variables
// passed down to F1 workers and inherited by F2 subagents.
func (c CapabilityHydration) EnvVars() []string {
	return []string{
		"G8S_ROLE=" + c.Role,
		"G8S_STRATUM=" + c.Stratum,
		"G8S_RECOMMENDED_SKILLS=" + strings.Join(c.RecommendedSkills, ","),
		"G8S_MCP_TOOLS_ALLOWED=" + strings.Join(c.AllowedMCPTools, ","),
	}
}
