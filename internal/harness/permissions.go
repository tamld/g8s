package harness

import (
	"fmt"
	"sort"
)

// PermissionProfile defines the access and mutation boundaries of a task.
type PermissionProfile struct {
	Name                   string `json:"name"`
	Description            string `json:"description"`
	MutationAllowed        bool   `json:"mutation_allowed"`
	SkipPermissionsAllowed bool   `json:"skip_permissions_allowed"`
	MaxPromptChars         int    `json:"max_prompt_chars"`
}

var Permissions = map[string]PermissionProfile{
	"read_only": {
		Name:                   "read_only",
		Description:            "Default profile. Read/list/summarize only. Uses OS sandbox by default.",
		MutationAllowed:        false,
		SkipPermissionsAllowed: false,
		MaxPromptChars:         30000,
	},
	"automation_read": {
		Name:                   "automation_read",
		Description:            "Read-only automation profile. Allows CLI permission skipping behind harness sandbox.",
		MutationAllowed:        false,
		SkipPermissionsAllowed: true,
		MaxPromptChars:         30000,
	},
	"workspace_write": {
		Name:                   "workspace_write",
		Description:            "Workspace mutation profile. Requires explicit Brain write receipt.",
		MutationAllowed:        true,
		SkipPermissionsAllowed: true,
		MaxPromptChars:         20000,
	},
}

// PermissionNames returns a sorted list of registered permission profile names.
func PermissionNames() []string {
	names := make([]string, 0, len(Permissions))
	for name := range Permissions {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

// GetPermission retrieves a PermissionProfile by name or returns an error.
func GetPermission(name string) (PermissionProfile, error) {
	perm, exists := Permissions[name]
	if !exists {
		return PermissionProfile{}, fmt.Errorf("unknown permission '%s'. Available: %v", name, PermissionNames())
	}
	return perm, nil
}
