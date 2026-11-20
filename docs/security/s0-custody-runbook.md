# S0 Human Key Custody Runbook (ADR-0042 TRUST-G2)

**Audience**: the S0 Principal (human operator) provisioning the disjoint Ed25519
key that authorizes Tier-B TCB mutations. **The S0 key NEVER leaves the
operator's control** — it is not a CI secret, not a vault item, not backed up to
any cloud service.

## 1. Why a second key exists

`F0` (coordinator Brain) is an LLM and holds `f0-signing.key` for receipts and
charters. Because frontier LLMs are prompt-injectable, ADR-0042 requires Tier-B
TCB approvals to be signed by a key F0 does NOT hold: the S0 human key
(`internal/signing/s0_keys.go` — `VerifyDisjointS0Key` refuses an S0 key equal
to the F0 key). Deliver-time enforcement: `cmd/g8s/deliver.go` verifies the S0
signature, target paths, artifact/checks digests, TTL and single-use nonce.

## 2. Provision (once per machine)

Key files live in the state dir (`~/.local/state/g8s/`), hex-encoded
(`parsePublicKeyHex` expects exactly 64 hex chars for the public key):

```bash
STATE="${G8S_STATE_DIR:-$HOME/.local/state/g8s}"
mkdir -p "$STATE" && chmod 700 "$STATE"

cat > /tmp/s0keygen.go <<'GO'
package main

import (
    "crypto/ed25519"
    "crypto/rand"
    "encoding/hex"
    "fmt"
    "os"
)

func main() {
    pub, priv, err := ed25519.GenerateKey(rand.Reader)
    if err != nil {
        panic(err)
    }
    if err := os.WriteFile(os.Args[1], []byte(hex.EncodeToString(priv)), 0o600); err != nil {
        panic(err)
    }
    if err := os.WriteFile(os.Args[2], []byte(hex.EncodeToString(pub)), 0o600); err != nil {
        panic(err)
    }
    fmt.Println("s0 keypair written:", os.Args[1], os.Args[2])
}
GO

go run /tmp/s0keygen.go "$STATE/s0-human.key" "$STATE/s0-human.pub" && rm /tmp/s0keygen.go
chmod 600 "$STATE/s0-human.key" "$STATE/s0-human.pub"
```

> **Known gap (follow-up candidate)**: there is no `g8s keys provision-s0` CLI
> yet — provisioning is the manual snippet above. Tracking: the runbook's
> companion issue (doctor-hint / provisioner command).

Verify disjointness from the F0 key and provisioning state:

```bash
./bin/g8s doctor --json | jq '.data.trust_assurance_status | {s0_key_provisioned, overall_verdict}'
# expect: s0_key_provisioned=true (and overall_verdict advances past the
# s0-key veto reason; other veto reasons may still apply in fast mode)
```

## 3. Custody rules

1. **0600 always**; the state dir is 0700 (enforced fail-closed by
   `internal/pathutil` for receipts/charters; keep the same hygiene for the
   S0 files).
2. **Never** commit, upload, sync, or paste the private key — no `.env`, no
   vault, no CI variable, no chat.
3. **One key per principal per machine-class** is acceptable; a leaked key is
   rotated immediately (§5).
4. The S0 private key is used ONLY to sign Tier-B approval attestations — it
   never signs receipts or charters (those are F0's authority).

## 4. Tier-B approval flow (how an approval reaches `g8s deliver`)

1. The coordinator proposes a TCB change (paths under
   `internal/sandbox|receipt|fsops|verifier`, `diff_firewall.go`,
   `scripts/install.sh`, `.g8s/trust-boundaries.yml`).
2. The operator reviews the concrete diff — approval is granted to an exact
   artifact: target paths + artifact digest + checks digest (all bound in the
   attestation, `internal/harness/trust_root.go:471-483`).
3. A signed `S0ApprovalAttestation` JSON is produced (nonce, window ≤24h,
   digest binding) and passed to `g8s deliver --s0-approval <file>`.
4. `deliver` verifies: signature → expiry window → clock skew → exact
   path-set equality → digest equality → **nonce consumed last** (single-use,
   `FileS0NonceStore`).

> **Known gap (follow-up candidate)**: no CLI signs the attestation today —
> `SignS0Approval` (`internal/signing/s0_keys.go:49`) is exercised only by
> doctor self-tests. Until the provisioner/signer command exists, producing a
> real attestation requires a small Go snippet calling
> `SignS0Approval(priv, fields...)` with the canonical field order from
> `s0ApprovalCanonicalFields` (`trust_root.go:471-483`).

## 5. Revocation / rotation

There is no server-side revocation list: **revocation = key replacement**.
1. Generate a new keypair (§2) under a temp name, verify disjointness, then:
   `mv s0-human.key.new s0-human.key && mv s0-human.pub.new s0-human.pub`.
2. In-flight approvals signed by the old key fail verification immediately at
   the next `deliver` (public-key pinning is per-file, checked at every use).
   In-flight tasks holding only receipts/charters (F0-signed) are unaffected.
3. Delete the old private key file; if compromise is suspected, treat every
   TCB change approved in the last 24h window (attestation TTL) as suspect and
   re-review.

## 6. Failure modes you will see

| Doctor / deliver signal | Meaning | Action |
| --- | --- | --- |
| `s0_key_provisioned=false`, verdict `UNVERIFIED` | no key at `<stateDir>/s0-human.pub` | §2 |
| `CodeTrustRootS0ApprovalRequired` | Tier-B mutation without an approval file | operator signs §4, rerun with `--s0-approval` |
| `CodeTrustRootS0SignatureInvalid` | attestation signed by wrong/rotated key | re-sign with current §2 key |
| `ErrS0KeyMatchesF0Key` | S0 key equals the F0 key (forbidden) | regenerate §2, never copy keys |
| nonce replay rejection | same approval used twice | issue a fresh approval (new nonce) |

Related: ADR-0042 (TRUST-G2), `internal/signing/s0_keys.go`,
`internal/harness/trust_root.go`, `cmd/g8s/deliver.go`.
