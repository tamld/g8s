package harness

import (
	"testing"

	"github.com/tamld/g8s/internal/sandbox"
)

func TestEvaluateTrustRootMutation_RedTests(t *testing.T) {
	tcbTargets := [][]string{
		{".g8s/trust-boundaries.yml"},
		{"internal/sandbox/sandbox.go"},
		{"internal/worker/diff_firewall.go"},
		{"f0-signing.key"},
		{"internal/verifier/holdout_partition.go"},
		{"scripts/install.sh"},
	}

	t.Run("f0_cannot_bypass_tier_b_or_self_approve_tcb", func(t *testing.T) {
		for _, paths := range tcbTargets {
			dec := EvaluateTrustRootMutation(TrustRootRequest{
				ActorStratum:              "F0",
				ActorID:                   "f0-brain",
				TargetPaths:               paths,
				RequestedTier:             TierABoundedAuto, // try to downgrade TCB edit into Tier A
				DeterministicChecksPassed: true,
			})
			if dec.Allowed || dec.EffectiveTier != TierBS0HumanTCB || !dec.TouchesTCB {
				t.Fatalf("expected Tier B block for F0 without S0 approval on %v, got %+v", paths, dec)
			}
			if dec.Code != CodeTrustRootS0ApprovalRequired {
				t.Fatalf("Code = %q on %v, want %q", dec.Code, paths, CodeTrustRootS0ApprovalRequired)
			}
		}

		// Forged S0Approval authored by F0/agent identity is blocked as self-attestation.
		forged := EvaluateTrustRootMutation(TrustRootRequest{
			ActorStratum:              "F0",
			ActorID:                   "f0-brain",
			TargetPaths:               []string{".g8s/trust-boundaries.yml"},
			DeterministicChecksPassed: true,
			S0Approval: &S0ApprovalAttestation{
				ApprovedBy:                "f0-brain",
				PrincipalStratum:          "S0",
				DeterministicChecksPassed: true,
				ChecksDigest:              "sha256:abc",
				ArtifactDigest:            "sha256:def",
			},
		})
		if forged.Allowed || forged.Code != CodeTrustRootSelfAttestationBlocked {
			t.Fatalf("expected %s on F0 self-attestation, got %+v", CodeTrustRootSelfAttestationBlocked, forged)
		}

		forgedAgent := EvaluateTrustRootMutation(TrustRootRequest{
			ActorStratum:              "S2",
			ActorID:                   "s2-governor",
			TargetPaths:               []string{"internal/sandbox/policy_contract.go"},
			DeterministicChecksPassed: true,
			S0Approval: &S0ApprovalAttestation{
				ApprovedBy:                "autonomous-agent-approver",
				PrincipalStratum:          "S0",
				DeterministicChecksPassed: true,
				ChecksDigest:              "sha256:abc",
				ArtifactDigest:            "sha256:def",
			},
		})
		if forgedAgent.Allowed || forgedAgent.Code != CodeTrustRootSelfAttestationBlocked {
			t.Fatalf("expected %s on agent-named attestor, got %+v", CodeTrustRootSelfAttestationBlocked, forgedAgent)
		}
	})

	t.Run("requires_deterministic_checks_even_with_valid_s0_human", func(t *testing.T) {
		noChecks := EvaluateTrustRootMutation(TrustRootRequest{
			ActorStratum:              "F0",
			ActorID:                   "f0-brain",
			TargetPaths:               []string{"internal/receipt/receipt.go"},
			DeterministicChecksPassed: false,
			S0Approval: &S0ApprovalAttestation{
				ApprovedBy:                "human:tamld",
				PrincipalStratum:          "S0",
				DeterministicChecksPassed: true,
				ChecksDigest:              "sha256:111",
				ArtifactDigest:            "sha256:222",
			},
		})
		if noChecks.Allowed || noChecks.Code != CodeTrustRootDeterministicChecksRequired {
			t.Fatalf("expected %s when deterministic checks fail, got %+v", CodeTrustRootDeterministicChecksRequired, noChecks)
		}

		missingDigest := EvaluateTrustRootMutation(TrustRootRequest{
			ActorStratum:              "F0",
			ActorID:                   "f0-brain",
			TargetPaths:               []string{"internal/receipt/receipt.go"},
			DeterministicChecksPassed: true,
			S0Approval: &S0ApprovalAttestation{
				ApprovedBy:                "human:tamld",
				PrincipalStratum:          "S0",
				DeterministicChecksPassed: true,
				ChecksDigest:              "",
				ArtifactDigest:            "sha256:222",
			},
		})
		if missingDigest.Allowed || missingDigest.Code != CodeTrustRootS0ApprovalRequired {
			t.Fatalf("expected %s when ChecksDigest is empty, got %+v", CodeTrustRootS0ApprovalRequired, missingDigest)
		}
	})

	t.Run("f1_worker_unconditionally_blocked_from_tcb_even_with_approval", func(t *testing.T) {
		workerAttempt := EvaluateTrustRootMutation(TrustRootRequest{
			ActorStratum:              "F1",
			ActorID:                   "worker-1",
			TargetPaths:               []string{"internal/sandbox/profile.go"},
			DeterministicChecksPassed: true,
			S0Approval: &S0ApprovalAttestation{
				ApprovedBy:                "human:tamld",
				PrincipalStratum:          "S0",
				DeterministicChecksPassed: true,
				ChecksDigest:              "sha256:111",
				ArtifactDigest:            "sha256:222",
			},
		})
		if workerAttempt.Allowed || workerAttempt.Code != CodeTrustRootWorkerTCBDenied {
			t.Fatalf("expected %s for F1 worker TCB attempt, got %+v", CodeTrustRootWorkerTCBDenied, workerAttempt)
		}
	})

	t.Run("valid_s0_human_plus_deterministic_checks_authorizes_tcb_and_tier_a_allows_non_tcb", func(t *testing.T) {
		validTCB := EvaluateTrustRootMutation(TrustRootRequest{
			ActorStratum:              "F0",
			ActorID:                   "f0-brain",
			TargetPaths:               []string{".g8s/trust-boundaries.yml", "internal/verifier/holdout_partition.go"},
			DeterministicChecksPassed: true,
			S0Approval: &S0ApprovalAttestation{
				ApprovedBy:                "human:tamld",
				PrincipalStratum:          "S0",
				DeterministicChecksPassed: true,
				ChecksDigest:              "sha256:ci-green",
				ArtifactDigest:            "sha256:pr-943",
			},
		})
		if !validTCB.Allowed || validTCB.EffectiveTier != TierBS0HumanTCB || validTCB.EpistemicClass != sandbox.EpistemicMeasuredMetric {
			t.Fatalf("expected valid Tier B TCB approval, got %+v", validTCB)
		}

		tierA := EvaluateTrustRootMutation(TrustRootRequest{
			ActorStratum:              "F0",
			ActorID:                   "f0-brain",
			TargetPaths:               []string{"docs/notes.md", "skills/custom/SKILL.md"},
			RequestedTier:             TierABoundedAuto,
			DeterministicChecksPassed: true,
		})
		if !tierA.Allowed || tierA.TouchesTCB || tierA.EffectiveTier != TierABoundedAuto {
			t.Fatalf("expected Tier A autonomous approval for non-TCB paths, got %+v", tierA)
		}
	})
}
