# T020: Concern A - Supervisor Persistence Concerns Resolution

## Overview
This document provides a comprehensive technical record of the resolution of Concern A, which identified critical architectural and operational issues within the `internal/supervisor` component related to persistence, clock injection, and architectural consistency.

## Problem Statement

### Identified Issues (Previously Documented)
1. **Circular Import Dependencies**: `internal/supervisor` incorrectly imported from `internal/controlplane`
2. **Time.Now() Violations**: Multiple hardcoded `time.Now()` calls preventing deterministic testing and injected clock functionality
3. **Type Mismatches**: Supervisor persistence using incorrect row types across packages
4. **Architectural Inconsistencies**: Supervisor layer not properly utilizing control-plane's own data structures
5. **Missing CLI Tools**: No `g8s orchestrate` and `g8s supervisor-metrics` commands for supervisor operations

## Root Cause Analysis

### 1. Architecture Design Flaw
The `internal/supervisor` package was incorrectly importing `internal/controlplane` instead of the reverse, creating a circular dependency that violated the separation of concerns and prevented proper abstraction layers.

### 2. Clock Injection Deficiencies
Multiple `time.Now()` calls throughout the supervisor layer prevented deterministic testing and broke the clean dependency injection pattern established in the rest of the codebase.

### 3. Type System Inconsistencies
Supervisor persistence operations used typed data structures from the wrong package (`supervisor.SupervisorTaskRow` instead of `controlplane.SupervisorTaskRow`), leading to type mismatches and compilation errors.

## Resolution Strategy

### 1. Architecture Correction
- Removed incorrect imports from `internal/supervisor`
- Fixed dependency direction to maintain proper separation of concerns
- Updated all type references to use correct package-qualified types

### 2. Clock Injection Implementation
- Added `clock` field to `Supervisor` struct
- Modified `AgyWorker` to accept injected clock in constructor
- Updated all timestamp generation to use injected clock functions
- Maintained backward compatibility with default `time.Now()` when no injection is provided

### 3. Type System Alignment
- Updated `internal/controlplane/supervisor_store.go` to use local row types instead of importing from `internal/supervisor`
- Fixed all function signatures to reference correct types
- Updated stub implementations to match control-plane interface requirements

### 4. CLI Tool Implementation
- Created `cmd/g8s/orchestrate.go`: Self-test mode running supervisor loops against real `agy` worker
- Created `cmd/g8s/supervisor_metrics.go`: Supervisor telemetry and aggregation reporting
- Updated `cmd/g8s/main.go` with new command registrations and usage documentation

### 5. Test Coverage Enhancement
- Added `TestSupervisorCrashRecovery`: Verifies orphaned task handling and recovery mechanisms
- Added `TestSupervisorLeaseExpiry`: Validates lease expiration detection and fresh task allocation
- Updated existing test suite to verify deterministic behavior with injected clocks

## Files Modified

### Core Architecture
- `internal/supervisor/supervisor.go` - Supervisor dependency injection and clock management
- `internal/supervisor/persist.go` - Persistence stub implementation with injected clocks
- `internal/supervisor/supervisor_test.go` - Supervisor test suite
- `internal/worker/stream.go` - Timestamp injection for stream events
- `internal/controlplane/supervisor_store.go` - Control-plane supervisor persistence using correct row types

### CLI Tools
- `cmd/g8s/main.go` - New command registrations for `orchestrate` and `supervisor-metrics`
- `cmd/g8s/orchestrate.go` - Self-test supervisor orchestration tool
- `cmd/g8s/supervisor_metrics.go` - Supervisor telemetry reporting tool

### Test Suite
- `internal/supervisor/recovery_test.go` - New recovery and lease expiry tests

## Technical Changes Summary

### Clock Injection Updates
1. **Supervisor Layer**: Added `clock func() time.Time` field to `Supervisor` struct
2. **AgyWorker**: Added `clock` parameter to constructor with fallback to `time.Now`
3. **Stream Events**: All stream event timestamps now use injected clocks
4. **Task ID Generation**: Supervisor task IDs now use injected clocks via global `supTaskClockFunc`

### Type System Alignment
1. **Control-Plane Persistence**: `supervisor_store.go` now uses local `SupervisorTaskRow`, `SupervisorDecisionRow`, and `MetricsRow` types
2. **Interface Compliance**: All persistence methods updated to reference correct types
3. **Stub Implementations**: Test persistence layers aligned with control-plane interfaces

### CLI Surface
1. **Orchestration Tool**: `g8s orchestrate --self-test` runs deterministic supervisor loops
2. **Metrics Tool**: `g8s supervisor-metrics` provides telemetry and aggregation
3. **Documentation**: Comprehensive command-line help and usage examples

## Verification Results

### Build Verification
```bash
# Standard Go build
$ go build ./...

# Architecture validation
$ go vet ./...

# Unit tests (CGO=0, deterministic clocks)
$ CGO_ENABLED=0 go test -count=1 ./internal/supervisor/... ./internal/controlplane/...

# Race detection (CGO=1, real-time simulation)
$ CGO_ENABLED=1 go test -count=1 -race ./internal/supervisor/...
```

### Test Results
- All existing supervisor tests continue to pass
- New recovery and lease expiry tests verify correct behavior
- No type mismatches or compilation errors
- Deterministic testing capabilities restored

### Self-Test Verification
```bash
# Self-test orchestration with real agy worker
$ go build -o /tmp/g8s ./cmd/g8s
$ /tmp/g8s orchestrate --self-test --max-attempts 3 --max-approaches 3 --add-dir ./src
```

## Design Principles Followed

### Zero-CGO Constitution
- All SQLite operations use `modernc.org/sqlite`
- Deterministic clock injection maintained throughout
- Pure Go implementation with no external dependencies

### Two-Tier Governance
- Brain layer (`internal/supervisor`) orchestrates strategic decisions
- Muscle layer (`agy`, control-plane) executes operational tasks
- Clear separation of concerns maintained

### Receipt-Based Write Delegation
- All persistence operations through typed interfaces
- Clock injection enables deterministic testing
- Audit trail preservation maintained

## Impact Assessment

### Positive Impacts
1. **Deterministic Testing**: Clock injection enables reproducible test scenarios
2. **Architectural Integrity**: Proper separation of concerns established
3. **Type Safety**: Correct type system eliminates compilation errors
4. **Test Coverage**: Enhanced test suite validates new functionality
5. **CLI Surface**: New commands provide operational visibility

### Risks Mitigated
1. **Circular Dependencies**: Architecture dependency direction corrected
2. **Testing Limitations**: Deterministic clocks enable comprehensive testing
3. **Type Errors**: Correct type system prevents compilation issues
4. **Operational Gaps**: CLI tools provide full operational capabilities

## Future Considerations

### Technical Debt
- The `supTaskClockFunc` global variable could be refactored to reduce package-level state
- Clock injection patterns could be extended to other components

### Enhancement Opportunities
- Integration with `internal/vault` for additional security features
- Expansion of `orchestrate` capabilities to support more complex workflows
- Addition of `supervisor-metrics` aggregation options for historical analysis

## Conclusion

The resolution of Concern A successfully addresses all identified architectural and operational issues while maintaining backward compatibility. The supervisor layer now provides proper persistence, deterministic testing capabilities, and a comprehensive CLI surface for operational tasks. The changes align with the Zero-CGO constitution and Two-Tier governance principles, establishing a robust foundation for supervisor-driven fix loops.

## Documentation Links
- OpenSpec Technical Deltas: `spec/openspec/`
- Architecture Design: `docs/ARCHITECTURE.md`
- Supervisor Package: `internal/supervisor/` (README and package-level documentation)
