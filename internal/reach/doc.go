// Package reach implements read-only platform data acquisition: an in-code
// channel registry, ordered fallback dispatch, and error classification for
// external research fetches. All backends are GET-only; see ADR-0037
// (docs/decisions/0037-reach-layer.md) for security invariants.
package reach
