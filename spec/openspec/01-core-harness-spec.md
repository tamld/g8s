# OpenSpec DELTA-01: Core Harness & Role Gates

**Status**: `APPLIED`  
**Milestone**: M1 (Foundation)  
**Package**: `internal/harness`  

---

## 1. Goal & Context
Implement the pre-dispatch and post-dispatch security harness in pure Go. It enforces 6 specialized worker roles, 3 permission profiles, blocked command regular expressions, sensitive path traversal guards, and contract prompt construction.

## 2. Interface Definition

```go
package harness

type RoleProfile struct {
    Name        string   `json:"name"`
    Purpose     string   `json:"purpose"`
    OutputFocus string   `json:"output_focus"`
    Forbidden   []string `json:"forbidden"`
}

type PermissionProfile struct {
    Name                   string `json:"name"`
    Description            string `json:"description"`
    MutationAllowed        bool   `json:"mutation_allowed"`
    SkipPermissionsAllowed bool   `json:"skip_permissions_allowed"`
    MaxPromptChars         int    `json:"max_prompt_chars"`
}

func ValidateRequest(
    prompt string,
    roleName string,
    permissionName string,
    addDirs []string,
    skipPermissions bool,
    receiptID string,
) error

func BuildContractPrompt(
    prompt string,
    roleName string,
    permissionName string,
    allowedPaths []string,
) (string, error)
```

## 3. Verification Criteria
- [x] All 6 roles defined: `collector`, `scout`, `mcp-mapper`, `summarizer`, `verifier`, `test-runner`.
- [x] All 3 permissions defined: `read_only`, `automation_read`, `workspace_write`.
- [x] `ValidateRequest` rejects blocked command patterns (`rm -rf`, `drop table`, `cat .env`).
- [x] `ValidateRequest` rejects sensitive path fragments (`.ssh`, `.aws`, `.env`, `id_rsa`).
- [x] `ValidateRequest` blocks `workspace_write` when `receiptID` is empty.
- [x] `BuildContractPrompt` injects exact allowed paths when receipt is present.
