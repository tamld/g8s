package dispatch

import (
	"runtime"
	"strings"

	"github.com/tamld/g8s/internal/harness"
)

// DefaultRole and DefaultPermission mirror the CLI argparse defaults.
const (
	DefaultRole       = "collector"
	DefaultPermission = "read_only"
)

// runtimeGOOS reports the host operating system for platform seams.
func runtimeGOOS() string {
	return runtime.GOOS
}

// normalize fills CLI-equivalent defaults for role and permission.
func (opts RunOptions) normalize() (role, permission string) {
	role = opts.Role
	if role == "" {
		role = DefaultRole
	}
	permission = opts.Permission
	if permission == "" {
		permission = DefaultPermission
	}
	return role, permission
}

// validateGate enforces the harness safety gate before execution.
func validateGate(opts RunOptions) error {
	role, permission := opts.normalize()
	return harness.ValidateRequest(
		opts.Prompt,
		role,
		permission,
		opts.AddDirs,
		opts.SkipPermissions,
		opts.ReceiptID,
	)
}

// buildPrompt wraps the user prompt in the enforced boundary contract.
func buildPrompt(opts RunOptions) (string, error) {
	role, permission := opts.normalize()
	return harness.BuildContractPrompt(opts.Prompt, role, permission, opts.AddDirs)
}

// permissionProfile resolves the effective profile for detection gating.
func permissionProfile(name string) (harness.PermissionProfile, error) {
	return harness.GetPermission(name)
}

// commandPreview renders a redacted argv summary; the prompt itself never
// appears in previews or logs.
func commandPreview(binary, model, timeout, effort string) string {
	preview := shellQuote(binary) +
		" --prompt <prompt>" +
		" --model " + shellQuote(model) +
		" --print-timeout " + shellQuote(timeout)
	if effort != "" {
		preview += " --effort " + shellQuote(effort)
	}
	return preview
}

// shellQuote applies POSIX single-quote escaping for anything outside the
// shlex-safe character set, matching the Python baseline's command previews.
func shellQuote(value string) string {
	const safeChars = "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789_@%+=:,./-"
	if value != "" && strings.Trim(value, safeChars) == "" {
		return value
	}
	return "'" + strings.ReplaceAll(value, "'", `'\''`) + "'"
}
