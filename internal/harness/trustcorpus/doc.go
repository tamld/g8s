// Package trustcorpus is the injection corpus v0 for the Context Authority
// Firewall (issue #977 PR-1; docs/security/context-authority-firewall-threat-model.md §3).
//
// The corpus is VERSIONED, CI-runnable data + runner. Each scenario couples a
// laundering vector from the threat model (§1, V2..V8) to a structural
// assertion on a deterministic sink or gate: the test drives the REAL module
// and asserts the EXECUTED ACTION (did the sink refuse? did the gate hold?),
// never a model self-report. Nothing here exercises an LLM; everything runs
// offline with no network and no real keys.
//
// Scenario registry (id → vector → sink under test):
//
//	forged-s0-prose             (V3) harness.EvaluateTrustRootMutation
//	tool-result-directive       (V2) lane.Router (Route + Suggest refusal)
//	policy-version-swap         (V4) lane trust-boundaries loading path
//	summary-provenance-loss     (V5) harness nonce/digest checks
//	memory-escalation-injection (V6) memory.ProposeKnowledgeArtifact / PromotionGate
//	reach-injection             (V7) context.Broker packet-level source partition
//	mcp-issuer-args             (V8) mcp g8s_charter_issue (lives in internal/mcp
//	                                 trustcorpus_test.go — the tool handler is
//	                                 package-internal; asserted in-package)
//
// PR-1 scope: assertions are structural only. Where current behavior is a
// documented GAP that PR-2/PR-3 must close, the test asserts the observed
// behavior, cites the gap id + issue, and skips with the observation recorded
// (the skip message is the corpus finding). No enforcement changes ship in
// PR-1 (threat model §5).
package trustcorpus
