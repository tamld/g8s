# Receipt Delegation Workflow

Write mutations in g8s are never carried through tool arguments. Instead the
orchestrator issues a **single-use, time-bounded write receipt** and the worker
consumes it exactly once during execution.

## End-to-end flow

1. **Issue** — the orchestrator creates a receipt scoped to explicit path
   patterns with a TTL (1..3600 seconds):

   ```sh
   g8s receipt-issue -issuer brain -path './src/**' -ttl 300
   ```

2. **Inject** — when a task runs under `workspace_write`, the worker embeds a
   delegated-write block into the contract prompt:

   ```text
   This task has DELEGATED WRITE permission via receipt.
   You may ONLY write to files matching these path patterns:
     - src/**
   Writing to ANY path outside this scope is a policy violation.
   Receipt ID: 3ebfcf1b-c754-4723-b899-e375771d471f
   Issuer: brain
   ```

3. **Validate + consume** — the worker validates the receipt against the store;
   consumption is atomic (exactly one winner). A second validate attempt fails
   with `write receipt already consumed: <id>`.

4. **Audit** — consumed receipts persist in the database; `ListActiveReceipts`
   only shows live ones, so drained receipts remain provable.

## Failure modes

| Situation | Behavior |
| --- | --- |
| Receipt expired | validation fails (`expired`) |
| Already consumed | `write receipt already consumed: <id>` |
| Revoked before use | `write receipt not found: <id>` |
| No receipt on a mutating task | contract prompt stays read-only; mutation is a policy violation |

## Lifecycle extras

- **Revoke**: deleting an unconsumed receipt invalidates it immediately.
- **Multi-agent safe**: two workers racing on one receipt yield exactly one winner.
