// Package supervisor — enforcer.go enforces the role/permission/allowed-paths
// contract on every RunRequest before the supervisor touches a worker.
package supervisor

import (
	"fmt"
	"strings"

	"github.com/tamld/g8s/internal/harness"
)

// Enforcer validates a RunRequest against the harness role + permission catalog
// before the supervisor dispatches it to a worker. The brain already validates
// the same fields once (in internal/harness.ValidateRequest) — the supervisor
// re-validates here so callers that bypass the brain (or unit tests) cannot
// smuggle an invalid request through.
type Enforcer struct {
	MinRole           string
	DefaultRole       string
	DefaultPermission string
}

// NewEnforcer returns an Enforcer with the safe defaults: collector +
// read_only. Callers that need different defaults construct one explicitly.
func NewEnforcer() *Enforcer {
	return &Enforcer{
		MinRole:           "collector",
		DefaultRole:       "collector",
		DefaultPermission: "read_only",
	}
}

// Validate returns nil only when every check passes. On failure the first
// broken invariant is reported as an error so the caller can surface a precise
// hint. The envelope is accepted (not re-validated) — its score is computed
// by the planner; the enforcer only checks the request body.
func (e *Enforcer) Validate(envelope TaskEnvelope, req RunRequest) error {
	_ = envelope // envelope is graded by the planner; enforcer only guards req

	if strings.TrimSpace(req.TaskDescription) == "" {
		return fmt.Errorf("enforcer: task description must not be empty")
	}

	roleName := req.Role
	if roleName == "" {
		roleName = e.DefaultRole
	}
	if _, err := harness.GetRole(roleName); err != nil {
		return fmt.Errorf("enforcer: %w", err)
	}
	if e.MinRole != "" {
		// ponytail: role-rank check is role-order-aware in the spec; we
		// only enforce "role must be known" here and let harness.GetRole
		// surface unknown-role errors. Add rank validation when spec
		// defines an authoritative role order.
		if _, err := harness.GetRole(e.MinRole); err != nil {
			return fmt.Errorf("enforcer: misconfigured MinRole: %w", err)
		}
	}

	permName := req.Permission
	if permName == "" {
		permName = e.DefaultPermission
	}
	perm, err := harness.GetPermission(permName)
	if err != nil {
		return fmt.Errorf("enforcer: %w", err)
	}

	if perm.MutationAllowed && len(req.AllowedFiles) == 0 {
		return fmt.Errorf("enforcer: permission=%s requires at least one AllowedFiles entry", perm.Name)
	}

	if strings.TrimSpace(req.Model) == "" {
		return fmt.Errorf("enforcer: Model must be non-empty")
	}

	if len(req.AddDirs) == 0 {
		return fmt.Errorf("enforcer: AddDirs must contain at least one directory")
	}

	return nil
}
