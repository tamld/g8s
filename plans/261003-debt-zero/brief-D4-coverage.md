# Task: D4 — coverage lift: initwiz + sleep + telemetry (each >= 80% on LINUX)

Repo: g8s @ main. CI-Linux: initwiz 67.9, sleep 68.2, telemetry 68.9.
Target: each >= 80%.

Same protocol as brief-D3-coverage.md (docker verification mandatory),
receipt scope: internal/initwiz/*_test.go, internal/sleep/*_test.go,
internal/telemetry/*_test.go. Note: sleep/telemetry likely have
timing/goroutine surfaces — keep tests deterministic (fake clocks,
injected tickers where the package allows; document any function that
is untestable-without-refactor and skip it honestly with a comment).
