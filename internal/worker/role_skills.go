package worker

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/tamld/g8s/internal/brief"
	"github.com/tamld/g8s/internal/dispatch"
)

const hydratedSkillsMarker = "--- HYDRATED SKILL CONTRACTS (DELTA-34) ---"

func needsEphemeralReadOnlyWorktree(req taskRequest, resolvedProvider ...string) bool {
	if req.Permission != "read_only" {
		return false
	}
	name := req.Provider
	if len(resolvedProvider) > 0 && strings.TrimSpace(resolvedProvider[0]) != "" {
		name = resolvedProvider[0]
	}
	return dispatch.SupportsWorktreeReadOnlyFirewall(name, "")
}

func roleSkillsForWorkerRole(role string) []string {
	switch strings.ToLower(strings.TrimSpace(role)) {
	case "verifier":
		return []string{"g8s-blind-verifier", "g8s-epistemic-audit"}
	case "qa-verifier":
		return []string{"g8s-scenario-matrix", "g8s-ux-visual-gate"}
	case "test-runner":
		return []string{"g8s-red-prove"}
	default:
		return nil
	}
}

func skillBankDirs(repoDir string) []string {
	var dirs []string
	if rd := strings.TrimSpace(repoDir); rd != "" {
		candidate := filepath.Join(rd, "skills")
		if st, err := os.Stat(candidate); err == nil && st.IsDir() {
			dirs = append(dirs, candidate)
		}
	}
	if bank := strings.TrimSpace(os.Getenv("G8S_SKILL_BANK")); bank != "" {
		dirs = append(dirs, bank)
	}
	return dirs
}

func presentRoleSkills(skillNames []string, bankDirs []string) []string {
	if len(skillNames) == 0 || len(bankDirs) == 0 {
		return nil
	}
	var present []string
	seen := make(map[string]bool)
	for _, name := range skillNames {
		clean := strings.TrimSpace(name)
		if clean == "" || seen[clean] {
			continue
		}
		found := false
		for _, bank := range bankDirs {
			if st, err := os.Stat(filepath.Join(bank, clean, "SKILL.md")); err == nil && !st.IsDir() {
				found = true
				break
			}
		}
		if found {
			seen[clean] = true
			present = append(present, clean)
		}
	}
	return present
}

// hydrateRoleSkills appends digest-pinned skill contracts for recognized worker
// roles when matching skills exist under repo-local skills/ or G8S_SKILL_BANK.
// Returns an error on digest verification failure (fail-closed).
func hydrateRoleSkills(req taskRequest, repoDir string) (string, error) {
	if strings.Contains(req.Prompt, hydratedSkillsMarker) {
		return req.Prompt, nil
	}
	bankDirs := skillBankDirs(repoDir)
	present := presentRoleSkills(roleSkillsForWorkerRole(req.Role), bankDirs)
	if len(present) == 0 {
		return req.Prompt, nil
	}
	block, _, err := brief.HydrateSkillsBlock(present, 2048, bankDirs...)
	if err != nil {
		return "", fmt.Errorf("E_SKILL_HYDRATION_FAILED: %w", err)
	}
	if strings.TrimSpace(block) == "" {
		return req.Prompt, nil
	}
	return strings.TrimRight(req.Prompt, "\n") + "\n\n" + block, nil
}

func resolveHydrationRepoDir(runRoot string, opts RunOptions, req taskRequest) string {
	explicit := strings.TrimSpace(firstOf(req.AddDirs))
	if explicit != "" && explicit != "." && (runRoot == "" || filepath.Clean(explicit) != filepath.Clean(runRoot)) {
		return explicit
	}
	return firstNonEmpty(opts.Dir, explicit, runRoot)
}
