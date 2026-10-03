# Task: D5 — coverage lift: probe + codeintel + doctor + service (each >= 80% on LINUX)

Repo: g8s @ main. CI-Linux: harness/probe 69.5, codeintel 70.1,
doctor 71.4, service 71.8. Target: each >= 80%.

Same protocol as brief-D3-coverage.md (docker verification mandatory),
receipt scope: internal/harness/probe/*_test.go,
internal/codeintel/*_test.go, internal/doctor/*_test.go,
internal/service/*_test.go. service is the HTTP daemon — use
httptest; doctor has disk/provider checks — hermetic temp dirs.
