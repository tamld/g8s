package brief

// #398: L2 deterministic DoR floor (blocks, no Jev, no broker dependency)
// + L4 advisory skill routing (keyword/manifest match, zero enforcement).
// Ratified split: mechanical floor blocks; Jev-judged quality is advisory
// and feature-flagged behind broker availability (never cold-start).

import (
	"fmt"
	"regexp"
	"strings"
	"time"

	"github.com/tamld/g8s/internal/reach"
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

// researchWorldStatePattern matches world-state, upstream, docs, or CVE/CWE research evidence (DELTA-35).
var researchWorldStatePattern = regexp.MustCompile(`(?i)\b(CVE-\d{4}-\d+|CWE-\d+|world[-_ ]state|upstream|research[_ -]?brief|local:|docs:|nvd|osv|advisory)\b`)

// securityAdvisoryPattern matches explicit security/CVE/CWE/STRIDE/OWASP/threat-model evidence required for Lane S (DELTA-35).
var securityAdvisoryPattern = regexp.MustCompile(`(?i)\b(CVE-\d{4}-\d+|CWE-\d+|no[-_ ]known[-_ ]cve|stride|owasp|threat[-_ ]model|trust[-_ ]boundary)\b`)

// socraticHypothesisPattern matches hypothesis/thesis framing in Socratic debate (DELTA-35).
var socraticHypothesisPattern = regexp.MustCompile(`(?i)\b(hypothesis|h1\b|h2\b|thesis|candidate[-_ ]approach)\b`)

// socraticFalsificationPattern matches counterexample/antithesis/falsification/rejected-alternative framing (DELTA-35).
var socraticFalsificationPattern = regexp.MustCompile(`(?i)\b(antithesis|counterexample|falsif\w*|alternative[s]?[-_ ]rejected|rejected[-_ ]alternative|trade[-_ ]?off|red[-_ ]team)\b`)

// specContractPattern matches formal specification contract anchors (PRD/SRS/AC/FSM/ADR/DELTA) (DELTA-35).
var specContractPattern = regexp.MustCompile(`(?i)\b(PRD\b|SRS\b|AC-\d+|REQ-[A-Z0-9-]+|US-\d+|FSM\b|ADR-\d+|DELTA-\d+|spec/openspec/)`)

// CheckDoRFloor runs the deterministic blocking floor over a brief draft.
// permission is the dispatch permission class ("" = read_only); a
// workspace_write dispatch must reference a receipt path.
func CheckDoRFloor(title, payload, dod, permission string) FloorResult {
	return CheckDoRFloorForLane(title, payload, dod, permission, "")
}

// CheckDoRFloorForLane runs the deterministic DoR floor plus the risk-tiered
// Pre-Execution Research, CVE & Socratic Debate Gate (DELTA-35) when lane is F, R, or S.
// Lanes "" (unset), "0", and "D" bypass the heavy pre-execution gate.
func CheckDoRFloorForLane(title, payload, dod, permission, lane string) FloorResult {
	checks := []FloorCheck{
		{Name: "goal_present", Pass: strings.TrimSpace(title) != "", Detail: "title must state the goal"},
		{Name: "scope_files_listed", Pass: pathLikePattern.MatchString(payload), Detail: "payload must list at least one concrete file path"},
		{Name: "dod_present", Pass: strings.TrimSpace(dod) != "", Detail: "definition of done must be present"},
	}
	combined := title + "\n" + payload + "\n" + dod
	if strings.EqualFold(permission, "workspace_write") {
		checks = append(checks, FloorCheck{
			Name:   "receipt_path_present",
			Pass:   receiptPattern.MatchString(payload + "\n" + dod),
			Detail: "workspace_write briefs must reference a write receipt path",
		})
	}

	normLane := strings.ToUpper(strings.TrimSpace(lane))
	if normLane == "F" || normLane == "R" || normLane == "S" {
		researchOK := researchWorldStatePattern.MatchString(combined)
		researchDetail := "lanes F/R/S require world-state/upstream/CVE research citations (e.g., Local:/Upstream:/Docs:/CVE-/CWE-)"
		if normLane == "S" {
			researchOK = researchOK && securityAdvisoryPattern.MatchString(combined)
			researchDetail = "lane S requires explicit CVE/CWE/STRIDE/OWASP/trust-boundary research evidence"
		}
		checks = append(checks,
			FloorCheck{
				Name:   "research_world_state_present",
				Pass:   researchOK,
				Detail: researchDetail,
			},
			FloorCheck{
				Name:   "socratic_debate_present",
				Pass:   socraticHypothesisPattern.MatchString(combined) && socraticFalsificationPattern.MatchString(combined),
				Detail: "lanes F/R/S require Socratic debate evidence (hypothesis + counterexample/falsification/rejected-alternative)",
			},
			FloorCheck{
				Name:   "spec_contract_present",
				Pass:   specContractPattern.MatchString(combined),
				Detail: "lanes F/R/S require formal spec contract reference (PRD, SRS, AC-*, FSM, ADR-*, or DELTA-*)",
			},
			checkReachCitationValid(combined),
		)
	}

	result := FloorResult{Checks: checks}
	result.OK = len(result.failures()) == 0
	return result
}

// FloorFailureError renders the failed checks as one actionable error.
func FloorFailureError(r FloorResult) error {
	return fmt.Errorf("brief DoR floor failed: %s", strings.Join(r.failures(), "; "))
}

func checkReachCitationValid(combined string) FloorCheck {
	const checkName = "reach_citation_valid"
	artifacts, parseErrs := reach.ParseReachCiteMarker(combined)
	if len(parseErrs) > 0 {
		return FloorCheck{Name: checkName, Pass: false, Detail: parseErrs[0].Error()}
	}
	if len(artifacts) == 0 {
		return FloorCheck{Name: checkName, Pass: true}
	}
	now := time.Now().UTC()
	for i, a := range artifacts {
		if err := reach.ValidateReachCitation(a, now, reach.MaxCitationAge); err != nil {
			return FloorCheck{
				Name:   checkName,
				Pass:   false,
				Detail: fmt.Sprintf("reach-cite marker %d: %v", i+1, err),
			}
		}
	}
	return FloorCheck{Name: checkName, Pass: true}
}
