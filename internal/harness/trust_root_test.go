package harness

import (
	"crypto/ed25519"
	"crypto/rand"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/tamld/g8s/internal/sandbox"
)

type testS0Fixture struct {
	s0Pub        ed25519.PublicKey
	s0Priv       ed25519.PrivateKey
	f0Pub        ed25519.PublicKey
	f0Priv       ed25519.PrivateKey
	now          time.Time
	checksDigest string
	artDigest    string
}

func newTestS0Fixture(t *testing.T) testS0Fixture {
	t.Helper()
	s0Pub, s0Priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatalf("GenerateKey s0: %v", err)
	}
	f0Pub, f0Priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatalf("GenerateKey f0: %v", err)
	}
	return testS0Fixture{
		s0Pub:        s0Pub,
		s0Priv:       s0Priv,
		f0Pub:        f0Pub,
		f0Priv:       f0Priv,
		now:          time.Date(2026, 11, 20, 1, 0, 0, 0, time.UTC),
		checksDigest: ComputeChecksDigest([]byte(`{"status":"succeeded","tests":"pass"}`)),
		artDigest:    ComputeChecksDigest([]byte("artifact-payload-v1")),
	}
}

func (f testS0Fixture) signedAttestation(t *testing.T, paths []string, nonce string) *S0ApprovalAttestation {
	t.Helper()
	att := &S0ApprovalAttestation{
		ApprovedBy:                "human:tamld",
		PrincipalStratum:          "S0",
		Nonce:                     nonce,
		IssuedAt:                  f.now.Add(-5 * time.Minute).Format(time.RFC3339),
		ExpiresAt:                 f.now.Add(1 * time.Hour).Format(time.RFC3339),
		TargetPaths:               paths,
		DeterministicChecksPassed: true,
		ChecksDigest:              f.checksDigest,
		ArtifactDigest:            f.artDigest,
	}
	if err := SignS0ApprovalAttestation(att, f.s0Priv); err != nil {
		t.Fatalf("SignS0ApprovalAttestation: %v", err)
	}
	return att
}

func TestEvaluateTrustRootMutation_RedTests(t *testing.T) {
	tcbTargets := [][]string{
		{".g8s/trust-boundaries.yml"},
		{"internal/sandbox/sandbox.go"},
		{"internal/worker/diff_firewall.go"},
		{"f0-signing.key"},
		{"s0-human.pub"},
		{"s0-human.key"},
		{"internal/verifier/holdout_partition.go"},
		{"scripts/install.sh"},
	}

	t.Run("f0_cannot_bypass_tier_b_or_self_approve_tcb", func(t *testing.T) {
		fx := newTestS0Fixture(t)
		for _, paths := range tcbTargets {
			dec := EvaluateTrustRootMutation(TrustRootRequest{
				ActorStratum:              "F0",
				ActorID:                   "f0-brain",
				TargetPaths:               paths,
				RequestedTier:             TierABoundedAuto, // try to downgrade TCB edit into Tier A
				DeterministicChecksPassed: true,
				S0PublicKey:               fx.s0Pub,
				F0PublicKey:               fx.f0Pub,
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
			S0PublicKey:               fx.s0Pub,
			F0PublicKey:               fx.f0Pub,
			S0Approval: &S0ApprovalAttestation{
				ApprovedBy:                "f0-brain",
				PrincipalStratum:          "S0",
				DeterministicChecksPassed: true,
				ChecksDigest:              fx.checksDigest,
				ArtifactDigest:            fx.artDigest,
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
			S0PublicKey:               fx.s0Pub,
			F0PublicKey:               fx.f0Pub,
			S0Approval: &S0ApprovalAttestation{
				ApprovedBy:                "autonomous-agent-approver",
				PrincipalStratum:          "S0",
				DeterministicChecksPassed: true,
				ChecksDigest:              fx.checksDigest,
				ArtifactDigest:            fx.artDigest,
			},
		})
		if forgedAgent.Allowed || forgedAgent.Code != CodeTrustRootSelfAttestationBlocked {
			t.Fatalf("expected %s on agent-named attestor, got %+v", CodeTrustRootSelfAttestationBlocked, forgedAgent)
		}
	})

	t.Run("requires_pinned_distinct_s0_key_and_valid_signature", func(t *testing.T) {
		fx := newTestS0Fixture(t)
		paths := []string{"internal/sandbox/policy_contract.go"}
		att := fx.signedAttestation(t, paths, "nonce-sig-redtest-0001")

		// Missing S0 pubkey fails closed.
		missingKey := EvaluateTrustRootMutation(TrustRootRequest{
			ActorStratum:              "F0",
			ActorID:                   "f0-brain",
			TargetPaths:               paths,
			DeterministicChecksPassed: true,
			ObservedChecksDigest:      fx.checksDigest,
			ObservedArtifactDigest:    fx.artDigest,
			F0PublicKey:               fx.f0Pub,
			Now:                       fx.now,
			S0Approval:                att,
		})
		if missingKey.Allowed || missingKey.Code != CodeTrustRootS0KeyMissing {
			t.Fatalf("expected %s, got %+v", CodeTrustRootS0KeyMissing, missingKey)
		}

		// S0 key colliding with F0 key fails closed.
		reusedKey := EvaluateTrustRootMutation(TrustRootRequest{
			ActorStratum:              "F0",
			ActorID:                   "f0-brain",
			TargetPaths:               paths,
			DeterministicChecksPassed: true,
			ObservedChecksDigest:      fx.checksDigest,
			ObservedArtifactDigest:    fx.artDigest,
			S0PublicKey:               fx.f0Pub,
			F0PublicKey:               fx.f0Pub,
			Now:                       fx.now,
			S0Approval:                att,
		})
		if reusedKey.Allowed || reusedKey.Code != CodeTrustRootS0KeyReuseForbidden {
			t.Fatalf("expected %s, got %+v", CodeTrustRootS0KeyReuseForbidden, reusedKey)
		}

		// Missing signature fails closed.
		unsignedAtt := *att
		unsignedAtt.Signature = ""
		unsigned := EvaluateTrustRootMutation(TrustRootRequest{
			ActorStratum:              "F0",
			ActorID:                   "f0-brain",
			TargetPaths:               paths,
			DeterministicChecksPassed: true,
			ObservedChecksDigest:      fx.checksDigest,
			ObservedArtifactDigest:    fx.artDigest,
			S0PublicKey:               fx.s0Pub,
			F0PublicKey:               fx.f0Pub,
			Now:                       fx.now,
			S0Approval:                &unsignedAtt,
		})
		if unsigned.Allowed || unsigned.Code != CodeTrustRootS0SignatureInvalid {
			t.Fatalf("expected %s for missing signature, got %+v", CodeTrustRootS0SignatureInvalid, unsigned)
		}

		// Compromised F0 signing with f0-signing.key fails against pinned S0 pubkey.
		f0SignedAtt := *att
		if err := SignS0ApprovalAttestation(&f0SignedAtt, fx.f0Priv); err != nil {
			t.Fatalf("SignS0ApprovalAttestation f0: %v", err)
		}
		f0Signed := EvaluateTrustRootMutation(TrustRootRequest{
			ActorStratum:              "F0",
			ActorID:                   "f0-brain",
			TargetPaths:               paths,
			DeterministicChecksPassed: true,
			ObservedChecksDigest:      fx.checksDigest,
			ObservedArtifactDigest:    fx.artDigest,
			S0PublicKey:               fx.s0Pub,
			F0PublicKey:               fx.f0Pub,
			Now:                       fx.now,
			S0Approval:                &f0SignedAtt,
		})
		if f0Signed.Allowed || f0Signed.Code != CodeTrustRootS0SignatureInvalid {
			t.Fatalf("expected %s for F0-signed attestation, got %+v", CodeTrustRootS0SignatureInvalid, f0Signed)
		}
	})

	t.Run("rejects_expired_excessive_ttl_future_issued_and_digest_or_path_tampering", func(t *testing.T) {
		fx := newTestS0Fixture(t)
		paths := []string{".g8s/trust-boundaries.yml"}

		noChecks := EvaluateTrustRootMutation(TrustRootRequest{
			ActorStratum:              "F0",
			ActorID:                   "f0-brain",
			TargetPaths:               paths,
			DeterministicChecksPassed: false,
			S0PublicKey:               fx.s0Pub,
			F0PublicKey:               fx.f0Pub,
			S0Approval:                fx.signedAttestation(t, paths, "nonce-nocheck-0001"),
		})
		if noChecks.Allowed || noChecks.Code != CodeTrustRootDeterministicChecksRequired {
			t.Fatalf("expected %s, got %+v", CodeTrustRootDeterministicChecksRequired, noChecks)
		}

		// Expired attestation.
		expAtt := &S0ApprovalAttestation{
			ApprovedBy:                "human:tamld",
			PrincipalStratum:          "S0",
			Nonce:                     "nonce-expired-0001",
			IssuedAt:                  fx.now.Add(-2 * time.Hour).Format(time.RFC3339),
			ExpiresAt:                 fx.now.Add(-1 * time.Minute).Format(time.RFC3339),
			TargetPaths:               paths,
			DeterministicChecksPassed: true,
			ChecksDigest:              fx.checksDigest,
			ArtifactDigest:            fx.artDigest,
		}
		if err := SignS0ApprovalAttestation(expAtt, fx.s0Priv); err != nil {
			t.Fatalf("sign expired: %v", err)
		}
		expired := EvaluateTrustRootMutation(TrustRootRequest{
			ActorStratum:              "F0",
			ActorID:                   "f0-brain",
			TargetPaths:               paths,
			DeterministicChecksPassed: true,
			ObservedChecksDigest:      fx.checksDigest,
			ObservedArtifactDigest:    fx.artDigest,
			S0PublicKey:               fx.s0Pub,
			F0PublicKey:               fx.f0Pub,
			Now:                       fx.now,
			S0Approval:                expAtt,
		})
		if expired.Allowed || expired.Code != CodeTrustRootS0Expired {
			t.Fatalf("expected %s for expired approval, got %+v", CodeTrustRootS0Expired, expired)
		}

		// Future IssuedAt beyond clock skew.
		futureAtt := &S0ApprovalAttestation{
			ApprovedBy:                "human:tamld",
			PrincipalStratum:          "S0",
			Nonce:                     "nonce-future-00001",
			IssuedAt:                  fx.now.Add(10 * time.Minute).Format(time.RFC3339),
			ExpiresAt:                 fx.now.Add(1 * time.Hour).Format(time.RFC3339),
			TargetPaths:               paths,
			DeterministicChecksPassed: true,
			ChecksDigest:              fx.checksDigest,
			ArtifactDigest:            fx.artDigest,
		}
		if err := SignS0ApprovalAttestation(futureAtt, fx.s0Priv); err != nil {
			t.Fatalf("sign future: %v", err)
		}
		futureDec := EvaluateTrustRootMutation(TrustRootRequest{
			ActorStratum:              "F0",
			ActorID:                   "f0-brain",
			TargetPaths:               paths,
			DeterministicChecksPassed: true,
			ObservedChecksDigest:      fx.checksDigest,
			ObservedArtifactDigest:    fx.artDigest,
			S0PublicKey:               fx.s0Pub,
			F0PublicKey:               fx.f0Pub,
			Now:                       fx.now,
			S0Approval:                futureAtt,
		})
		if futureDec.Allowed || futureDec.Code != CodeTrustRootS0Expired {
			t.Fatalf("expected %s for future issued_at, got %+v", CodeTrustRootS0Expired, futureDec)
		}

		// Tampered observed artifact digest.
		validAtt := fx.signedAttestation(t, paths, "nonce-digest-00001")
		badArtifact := EvaluateTrustRootMutation(TrustRootRequest{
			ActorStratum:              "F0",
			ActorID:                   "f0-brain",
			TargetPaths:               paths,
			DeterministicChecksPassed: true,
			ObservedChecksDigest:      fx.checksDigest,
			ObservedArtifactDigest:    ComputeChecksDigest([]byte("tampered-artifact")),
			S0PublicKey:               fx.s0Pub,
			F0PublicKey:               fx.f0Pub,
			Now:                       fx.now,
			S0Approval:                validAtt,
		})
		if badArtifact.Allowed || badArtifact.Code != CodeTrustRootDigestMismatch {
			t.Fatalf("expected %s for artifact digest mismatch, got %+v", CodeTrustRootDigestMismatch, badArtifact)
		}

		// Smuggled extra target path outside signed target_paths.
		smuggled := EvaluateTrustRootMutation(TrustRootRequest{
			ActorStratum:              "F0",
			ActorID:                   "f0-brain",
			TargetPaths:               []string{".g8s/trust-boundaries.yml", "internal/sandbox/sandbox.go"},
			DeterministicChecksPassed: true,
			ObservedChecksDigest:      fx.checksDigest,
			ObservedArtifactDigest:    fx.artDigest,
			S0PublicKey:               fx.s0Pub,
			F0PublicKey:               fx.f0Pub,
			Now:                       fx.now,
			S0Approval:                validAtt,
		})
		if smuggled.Allowed || smuggled.Code != CodeTrustRootTargetPathsMismatch {
			t.Fatalf("expected %s when smuggled path added, got %+v", CodeTrustRootTargetPathsMismatch, smuggled)
		}
	})

	t.Run("f1_worker_unconditionally_blocked_from_tcb_even_with_valid_signature", func(t *testing.T) {
		fx := newTestS0Fixture(t)
		paths := []string{"internal/sandbox/profile.go"}
		workerAttempt := EvaluateTrustRootMutation(TrustRootRequest{
			ActorStratum:              "F1",
			ActorID:                   "worker-1",
			TargetPaths:               paths,
			DeterministicChecksPassed: true,
			ObservedChecksDigest:      fx.checksDigest,
			ObservedArtifactDigest:    fx.artDigest,
			S0PublicKey:               fx.s0Pub,
			F0PublicKey:               fx.f0Pub,
			Now:                       fx.now,
			S0Approval:                fx.signedAttestation(t, paths, "nonce-worker-00001"),
		})
		if workerAttempt.Allowed || workerAttempt.Code != CodeTrustRootWorkerTCBDenied {
			t.Fatalf("expected %s for F1 worker TCB attempt, got %+v", CodeTrustRootWorkerTCBDenied, workerAttempt)
		}
	})

	t.Run("valid_signed_s0_authorizes_once_dryrun_non_consuming_and_replay_blocked", func(t *testing.T) {
		fx := newTestS0Fixture(t)
		paths := []string{"internal/verifier/holdout_partition.go", ".g8s/trust-boundaries.yml"}
		att := fx.signedAttestation(t, []string{".g8s/trust-boundaries.yml", "internal/verifier/holdout_partition.go"}, "nonce-once-0000001")

		noncePath := filepath.Join(t.TempDir(), "s0-used-nonces.json")
		store := NewFileS0NonceStore(noncePath)

		// DryRun succeeds and does NOT burn the nonce.
		dry := EvaluateTrustRootMutation(TrustRootRequest{
			ActorStratum:              "F0",
			ActorID:                   "f0-brain",
			TargetPaths:               paths,
			DeterministicChecksPassed: true,
			ObservedChecksDigest:      fx.checksDigest,
			ObservedArtifactDigest:    fx.artDigest,
			S0PublicKey:               fx.s0Pub,
			F0PublicKey:               fx.f0Pub,
			Now:                       fx.now,
			NonceStore:                store,
			DryRun:                    true,
			S0Approval:                att,
		})
		if !dry.Allowed || dry.EffectiveTier != TierBS0HumanTCB {
			t.Fatalf("expected valid DryRun TCB approval, got %+v", dry)
		}

		// Real execution consumes the nonce.
		live := EvaluateTrustRootMutation(TrustRootRequest{
			ActorStratum:              "F0",
			ActorID:                   "f0-brain",
			TargetPaths:               paths,
			DeterministicChecksPassed: true,
			ObservedChecksDigest:      fx.checksDigest,
			ObservedArtifactDigest:    fx.artDigest,
			S0PublicKey:               fx.s0Pub,
			F0PublicKey:               fx.f0Pub,
			Now:                       fx.now,
			NonceStore:                store,
			DryRun:                    false,
			S0Approval:                att,
		})
		if !live.Allowed || live.EffectiveTier != TierBS0HumanTCB || live.EpistemicClass != sandbox.EpistemicMeasuredMetric {
			t.Fatalf("expected valid live Tier B TCB approval, got %+v", live)
		}

		// Reload file-backed store cleanly and verify replay fails closed.
		reloadedStore := NewFileS0NonceStore(noncePath)
		replay := EvaluateTrustRootMutation(TrustRootRequest{
			ActorStratum:              "F0",
			ActorID:                   "f0-brain",
			TargetPaths:               paths,
			DeterministicChecksPassed: true,
			ObservedChecksDigest:      fx.checksDigest,
			ObservedArtifactDigest:    fx.artDigest,
			S0PublicKey:               fx.s0Pub,
			F0PublicKey:               fx.f0Pub,
			Now:                       fx.now,
			NonceStore:                reloadedStore,
			DryRun:                    false,
			S0Approval:                att,
		})
		if replay.Allowed || replay.Code != CodeTrustRootS0ReplayDetected {
			t.Fatalf("expected %s on replay after reload, got %+v", CodeTrustRootS0ReplayDetected, replay)
		}

		// Also verify MemoryS0NonceStore blocks replay after single consume.
		mem := NewMemoryS0NonceStore()
		reqMem := TrustRootRequest{
			ActorStratum:              "F0",
			ActorID:                   "f0-brain",
			TargetPaths:               paths,
			DeterministicChecksPassed: true,
			ObservedChecksDigest:      fx.checksDigest,
			ObservedArtifactDigest:    fx.artDigest,
			S0PublicKey:               fx.s0Pub,
			F0PublicKey:               fx.f0Pub,
			Now:                       fx.now,
			NonceStore:                mem,
			S0Approval:                att,
		}
		if d1 := EvaluateTrustRootMutation(reqMem); !d1.Allowed {
			t.Fatalf("expected first MemoryS0NonceStore call to allow, got %+v", d1)
		}
		if d2 := EvaluateTrustRootMutation(reqMem); d2.Allowed || d2.Code != CodeTrustRootS0ReplayDetected {
			t.Fatalf("expected replay rejection in MemoryS0NonceStore, got %+v", d2)
		}

		// Non-TCB path stays Tier A autonomous when deterministic checks pass.
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

func TestComputeDeliverableArtifactDigestDeterministicAndSensitive(t *testing.T) {
	wt := t.TempDir()
	sub := filepath.Join(wt, "internal", "sandbox")
	if err := os.MkdirAll(sub, 0o700); err != nil {
		t.Fatal(err)
	}
	f1 := filepath.Join(sub, "a.go")
	f2 := filepath.Join(sub, "b.go")
	if err := os.WriteFile(f1, []byte("package sandbox\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(f2, []byte("package sandbox\nfunc X() {}\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	d1, err := ComputeDeliverableArtifactDigest(wt, []string{"internal/sandbox/b.go", "./internal/sandbox/a.go"}, []string{"internal/sandbox/old.go"})
	if err != nil {
		t.Fatalf("digest 1: %v", err)
	}
	d2, err := ComputeDeliverableArtifactDigest(wt, []string{"internal/sandbox/a.go", "internal/sandbox/b.go"}, []string{"./internal/sandbox/old.go"})
	if err != nil {
		t.Fatalf("digest 2: %v", err)
	}
	if d1 == "" || !IsValidSHA256Digest(d1) || d1 != d2 {
		t.Fatalf("expected identical valid sha256 digest across path order, got d1=%q d2=%q", d1, d2)
	}

	// 1-byte mutation must change digest.
	if err := os.WriteFile(f2, []byte("package sandbox\nfunc Y() {}\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	d3, err := ComputeDeliverableArtifactDigest(wt, []string{"internal/sandbox/a.go", "internal/sandbox/b.go"}, []string{"internal/sandbox/old.go"})
	if err != nil {
		t.Fatalf("digest 3: %v", err)
	}
	if d3 == d1 {
		t.Fatal("expected 1-byte change to alter deliverable artifact digest")
	}

	// Changing deleted file list must change digest.
	d4, err := ComputeDeliverableArtifactDigest(wt, []string{"internal/sandbox/a.go", "internal/sandbox/b.go"}, nil)
	if err != nil {
		t.Fatalf("digest 4: %v", err)
	}
	if d4 == d3 {
		t.Fatal("expected deleted file removal to alter deliverable artifact digest")
	}
}
