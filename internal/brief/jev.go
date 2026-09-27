package brief

// L2 Jev-judged quality seam (#398, DELTA-22 requirement 3): the
// "is this brief well-formed for this situation?" check is advisory only
// and gated on broker availability. Cold-start scoring without context is
// forbidden (ADR-0021 §8): no hook wired → the flag alone does nothing
// observable except an explicit unavailability notice.

import (
	"fmt"
	"os"
)

// JevQualityHook scores a brief for situational well-formedness. It is
// wired by the Context Broker integration when telemetry distillation makes
// scoring meaningful; nil in v1.
var JevQualityHook func(title, payload string) (verdict string, err error)

// ApplyJevQuality runs the advisory hook when the operator flag
// (G8S_BRIEF_JEV_QUALITY=1) and a wired hook are both present. It never
// blocks issuance: the verdict is returned for display, errors are
// degradation notices.
func ApplyJevQuality(title, payload string) (verdict string, err error) {
	if os.Getenv("G8S_BRIEF_JEV_QUALITY") != "1" {
		return "", nil
	}
	if JevQualityHook == nil {
		return "", fmt.Errorf("jev quality unavailable: broker hook not wired (cold-start scoring is forbidden)")
	}
	return JevQualityHook(title, payload)
}
