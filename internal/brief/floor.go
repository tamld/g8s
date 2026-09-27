package brief

// #398: L2 deterministic DoR floor (blocks, no Jev, no broker dependency)
// + L4 advisory skill routing (keyword/manifest match, zero enforcement).
// Ratified split: mechanical floor blocks; Jev-judged quality is advisory
// and feature-flagged behind broker availability (never cold-start).

import (
	"fmt"
	"regexp"
	"strings"
)

// FloorCheck is one mechanical DoR check result.
type FloorCheck struct {
	Name   string `json:"name"`
	Pass   bool   `json:"pass"`
	Detail string `json:"detail,omitempty"`
}

// FloorResult is the aggregate of all floor checks.
type FloorResult struct {
	OK     bool         `json:"ok"`
	Checks []FloorCheck `json:"checks"`
}

func (r FloorResult) failures() []string {
	var out []string
	for _, c := range r.Checks {
		if !c.Pass {
			out = append(out, c.Name+": "+c.Detail)
		}
	}
	return out
}

// pathLikePattern matches a concrete file path token (the mechanical
// "scope files listed" test — a path with an extension, or an explicit
// directory scope ending in /).
var pathLikePattern = regexp.MustCompile(`[A-Za-z0-9_./-]+\.[A-Za-z0-9]{1,8}\b|[\w-]+/`)

// receiptPattern matches a receipt reference in the brief text.
var receiptPattern = regexp.MustCompile(`(?i)\breceipt[_ -]?(id|path|ref)?\b|receipt_id=|g8s receipt`)

// CheckDoRFloor runs the deterministic blocking floor over a brief draft.
// permission is the dispatch permission class ("" = read_only); a
// workspace_write dispatch must reference a receipt path.
func CheckDoRFloor(title, payload, dod, permission string) FloorResult {
	checks := []FloorCheck{
		{Name: "goal_present", Pass: strings.TrimSpace(title) != "", Detail: "title must state the goal"},
		{Name: "scope_files_listed", Pass: pathLikePattern.MatchString(payload), Detail: "payload must list at least one concrete file path"},
		{Name: "dod_present", Pass: strings.TrimSpace(dod) != "", Detail: "definition of done must be present"},
	}
	if strings.EqualFold(permission, "workspace_write") {
		combined := payload + "\n" + dod
		checks = append(checks, FloorCheck{
			Name:   "receipt_path_present",
			Pass:   receiptPattern.MatchString(combined),
			Detail: "workspace_write briefs must reference a write receipt path",
		})
	}
	result := FloorResult{Checks: checks}
	result.OK = len(result.failures()) == 0
	return result
}

// FloorFailureError renders the failed checks as one actionable error.
func FloorFailureError(r FloorResult) error {
	return fmt.Errorf("brief DoR floor failed: %s", strings.Join(r.failures(), "; "))
}
