# Task: Generate comprehensive SAFETY & COORDINATION tests for AGY Dispatch

Read these files in `/Users/tamld/plugins/agy-dispatch/scripts/`:
1. `agy_harness.py` — focus on `build_contract_prompt()` (now accepts `receipt_info`), `validate_dispatch()` (receipt gate), wiki engine policy in prompt
2. `agy_control_plane.py` — focus on `revoke_write_receipt()`, `list_active_receipts()`, `issue_write_receipt()`, `validate_write_receipt()`
3. `agy_dispatch.py` — focus on how receipt_info flows to prompt builder and result JSON

## CRITICAL CONTEXT

The system has a Brain (Opus) orchestrating Flash workers. Safety gaps identified:

1. **allowed_paths is now injected into worker prompt** — need tests proving it appears
2. **Wiki engine policy now in prompt** — read-only workers see FORBIDDEN list for wiki.py write/reflect/orient
3. **revoke_receipt()** — Brain can cancel receipt before worker uses it
4. **list_active_receipts()** — Brain can audit outstanding receipts
5. **Multi-agent coordination** — workers share EVENT_LOG, notes, git state

## Generate: `test_safety_coordination.py`

### Category A: Prompt Scope Injection (6+ tests)
- Read-only worker prompt contains wiki.py FORBIDDEN list
- Read-only worker prompt does NOT contain "DELEGATED WRITE"
- workspace_write worker prompt with receipt contains allowed_paths list
- workspace_write worker prompt with receipt contains receipt_id
- workspace_write worker prompt with receipt contains issuer
- workspace_write worker prompt without receipt (generic mutation line)
- Verify exact strings: "wiki.py write" in FORBIDDEN, "wiki.py query" in ALLOWED

### Category B: Receipt Revocation (5+ tests)
- Issue → revoke → validate should fail (receipt not found)
- Revoke non-existent receipt returns False
- Revoke already-consumed receipt returns False
- Revoke then re-issue with same allowed_paths → new receipt_id
- Issue → consume → revoke returns False (too late)
- Issue → revoke → list_active_receipts doesn't include it

### Category C: Active Receipt Listing (4+ tests)
- list_active_receipts with zero receipts → empty list
- Issue 3 receipts, consume 1, expire 1 (mock clock) → list shows only 1
- list_active_receipts shows remaining_seconds correctly
- list_active_receipts after revoke → receipt gone

### Category D: Multi-Agent Coordination Simulation (6+ tests)
- Two workers issue+validate receipts from same ControlPlane DB → independent
- Worker A validates receipt while Worker B validates different receipt → both succeed
- Worker A and Worker B try to validate SAME receipt → only one succeeds (race)
- Brain issues receipt, Worker validates, Brain calls list_active → consumed doesn't appear
- Brain issues 10 receipts for 10 workers, each validates independently → all succeed
- Receipts from previous session (expired) don't appear in list_active

### Category E: Contract Prompt Security (5+ tests)  
- Prompt with receipt_info=None + workspace_write → generic mutation line (no paths)
- Prompt with receipt_info containing empty allowed_paths list → still injects block
- Verify FORBIDDEN wiki commands are listed for ALL read-only permission profiles
- Verify ALLOWED wiki commands include query, search, read, classify
- Prompt injection attempt in allowed_paths → paths appear as literal strings, not executable

### Category F: End-to-End Orchestration Flow (3+ tests)
- Full flow: issue receipt → validate_dispatch → build_contract_prompt → verify prompt contains paths
- Full flow: issue → revoke → validate_dispatch with revoked receipt → ValueError
- Full flow: issue → list_active (1 result) → consume → list_active (0 results)

Output the COMPLETE Python test file. Use same fixtures pattern as test_receipt_delegation.py.
