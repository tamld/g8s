// Sandbox enforcement (workspace_write) metric loader (#971 WP2).
//
// Reads finished workspace_write attempt envelopes from the durable tasks
// table and buckets them by enforcement outcome via ClassifySandboxOutcome.
// The caller owns opening the *sql.DB (and registering the sqlite driver);
// this package only issues the read-only query, keeping the classification
// logic pure and shared between tools/dogfood_provenance and
// cmd/g8s scorecard compliance_vs_quality telemetry.

package dogfood

import (
	"context"
	"database/sql"
	"fmt"
)

// LoadSandboxEnforcement counts workspace_write attempts by enforcement
// outcome over tasks created since sinceUnix (0 = no time filter). It returns
// an error only when the query itself fails; a missing tasks table surfaces as
// an error from the query layer.
func LoadSandboxEnforcement(ctx context.Context, db *sql.DB, sinceUnix float64) (SandboxEnforcementSummary, error) {
	if db == nil {
		return SandboxEnforcementSummary{}, fmt.Errorf("sandbox enforcement: nil tasks database handle")
	}
	rows, err := db.QueryContext(ctx, `
SELECT COALESCE(NULLIF(TRIM(json_extract(request_json,'$.permission')), ''), ''),
       COALESCE(result_json, '')
FROM tasks
WHERE request_json IS NOT NULL AND TRIM(request_json) != ''
  AND json_extract(request_json,'$.permission') = 'workspace_write'
  AND (? <= 0 OR created_at >= ?)`,
		sinceUnix, sinceUnix,
	)
	if err != nil {
		return SandboxEnforcementSummary{}, fmt.Errorf("sandbox enforcement query: %w", err)
	}
	defer rows.Close()

	var outcomes []SandboxEnforcementOutcome
	for rows.Next() {
		var perm, result string
		if scanErr := rows.Scan(&perm, &result); scanErr != nil {
			return SandboxEnforcementSummary{}, fmt.Errorf("sandbox enforcement scan: %w", scanErr)
		}
		outcomes = append(outcomes, ClassifySandboxOutcome(perm, result))
	}
	if err := rows.Err(); err != nil {
		return SandboxEnforcementSummary{}, fmt.Errorf("sandbox enforcement rows: %w", err)
	}
	return SummarizeSandboxEnforcement(outcomes), nil
}
