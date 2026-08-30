# AGY Worker Execution Policy & Boundary Governance (DEBT-63 / Issue #210)

> **Authority**: g8s Zero-Trust Capability & Process Governance Model  
> **Target**: AGY Subagent Workers, Automated Dispatchers, and Multi-Agent Harnesses  
> **Status**: Mandatory Invariant Policy  

---

## 1. Core Operating Principles

As an AGY worker in the `g8s` ecosystem, you operate under strict **Two-Tier Governance** (ADR-0001) and **Zero-Trust Capability Delegation** (ADR-0002). Intelligence directs; muscle executes; harness protects; runtime proves.

You must strictly abide by the invariants, operational boundaries, and deny-list detailed below.

---

## 2. Explicit Deny-List (Forbidden Operations)

Under NO circumstances may an AGY worker attempt or execute any of the following operations:

1. **No Direct Pushes to Main or Protected Branches**:
   - `git push origin main`
   - `git push --force origin main` or `git push -f`
   - All code changes MUST be committed to feature/fix branches and submitted via GitHub Pull Requests.

2. **No Automated PR Merging**:
   - `gh pr merge --auto`
   - Auto-merging bypasses supervisor review. Merges are exclusively performed by the repository supervisor (Sisyphus) after all CI gates pass.

3. **No Altering Branch Protections**:
   - `gh api -X PATCH /repos/.../branches/main/protection`
   - Modifying or disabling GitHub repository rules, branch protection, or status check gates is strictly prohibited.

4. **No Force-Deleting Branches**:
   - `git branch -D <branch>`
   - Use safe branch deletion (`git branch -d`) only. Force-deletions risk destroying unmerged worktrees.

5. **No Destructive Operations Outside CWD**:
   - `rm -rf /`, `rm -rf /tmp`, `rm -rf $HOME`, `rm -rf ../`
   - Filesystem modifications must remain strictly scoped to the allocated workspace/worktree root.

6. **No Fabricated Test Symbols or TDD Traps**:
   - Writing tests that reference hallucinated, unexported, or undefined symbols before production design (DEBT-49).
   - Asserting against private, unexported struct fields (locks-impl-detail).

7. **No Local Filesystem Path Leaks**:
   - Leaking absolute host paths (e.g. `/Users/...`, `/home/...`, `/private/var/...`) in committed code, docs, or briefs (DEBT-61). Use `$HOME` or repo-relative paths.

---

## 3. Mandatory Execution Invariants

1. **Verify the Receipt Before Declaring Success**:
   - Never assume exit code 0 implies success. Always inspect the stdout envelope and verify execution receipts via `ReceiptVerifier` / `StdoutEnvelopeVerifier`.
   - If stdout contains an error envelope (`"kind": "error"`), report the error and mark the task failed.

2. **Never Claim Shared Resources as Exclusively Yours**:
   - Never claim a worktree, branch, issue, or PR is exclusively yours.
   - Respect multi-agent provenance and attribution.

3. **Always Close Issues When Resolved**:
   - Always reference the exact issue IDs in your commit messages and PR descriptions (e.g. `Fixes #194, Fixes #195, Fixes #196, Fixes #208, Fixes #209, Fixes #210, Fixes #211`).
   - If you opened an issue and resolved it in your PR, ensure it is properly closed upon merge.

4. **Maintain Pure-Go & Zero-CGO Invariants**:
   - Ensure all code compiles cleanly with `CGO_ENABLED=0 go test ./...`.
   - Never introduce dynamic C runtime dependencies or unpinned third-party tools.
