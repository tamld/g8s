# Coordinator core, adapters and CLI integration

Status: pending phase 1. Integration is a temporary task role available to any peer under D2. Initial implementation supervisors: Zcode and AGY. Each supervisor dispatches through g8s; no native host-model worker clones.

## Work packages

| Packet | Supervisor | Owned paths | Acceptance |
|---|---|---|---|
| Core registry/inbox/FSM | Zcode | Six files under internal/mesh named in design.md | Protocol negative controls, SQLite restart/dedup/capacity tests, targeted race tests |
| Awareness and attention | AGY | Four files under internal/supervisor named in design.md | Three-direction snapshot, unknown quota, expired observations, unavailable/busy adapter and exact-session binding tests |
| Public CLI and declaration integration | Temporary integration holder; Codex initially prepares the packet | Exact cmd/g8s, internal/cli and documentation/declaration files frozen in its own brief | CLI/MCP parity, symmetric role enforcement, feature ownership, spec lineage and native command integration tests |

All paths are relative to the bound isolated worktree. Every live brief supplies absolute workdir and scope root. Worktree delivery roots must match `pwd` and Git top-level. Receipt paths are narrow and never wildcard the repository. Test output and receipts are independently checked before acceptance.

## Order and interfaces

- [ ] Complete the phase 1 typed interface freeze and write-capability admission.
- [ ] Issue independent receipts/worktrees for core and adapters.
- [ ] Run disjoint core and adapter packets; workers do not review each other.
- [ ] Verify physical changes, targeted tests and declared check counts.
- [ ] Integrate CLI/declarations in a separate layer-disjoint packet.
- [ ] Rebuild and doctor the resulting binary before dispatching the native drill.

Core code may not import platform GUI/Orca details. Adapter code may not change inbox authority or execute arbitrary commands received in messages. Quality telemetry cannot become quota. Process liveness cannot trigger reassignment in v1. Existing MCP response shapes remain compatible unless an explicit spec changes them.

Commands to verify actual implemented packages include targeted `go test -race -count=1`, `go vet`, feature ownership, spec/code and doc-contract checks. Test names and package lists are frozen in each brief; no zero-match regex passes. Full checks are reported with actual evidence, not promised here.
