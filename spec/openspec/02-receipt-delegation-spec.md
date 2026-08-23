# OpenSpec DELTA-02: Receipt-Based Write Delegation

**Status**: `PROPOSED`  
**Milestone**: M1 (Foundation)  
**Package**: `internal/receipt`  

---

## 1. Goal & Context
Implement the capability delegation gate that allows Brain orchestrators to issue time-limited ($\le 3600s$), path-scoped (`allowed_paths` glob) write receipts to workers. Receipts are stored in SQLite and consumed atomically upon single-use validation.

## 2. Interface Definition

```go
package receipt

import "time"

type WriteReceipt struct {
    ReceiptID      string    `json:"receipt_id"`
    Issuer         string    `json:"issuer"`
    AllowedPaths   []string  `json:"allowed_paths"`
    ExpiresAt      time.Time `json:"expires_at"`
    Consumed       bool      `json:"consumed"`
    ConsumerTaskID *string   `json:"consumer_task_id,omitempty"`
    CreatedAt      time.Time `json:"created_at"`
}

type ReceiptManager interface {
    IssueReceipt(issuer string, allowedPaths []string, ttl time.Duration) (*WriteReceipt, error)
    ValidateAndConsume(receiptID string, consumerTaskID string) (*WriteReceipt, error)
    RevokeReceipt(receiptID string) (bool, error)
    ListActiveReceipts() ([]*WriteReceipt, error)
}
```

## 3. Security Rules
1. `allowedPaths` must not be empty.
2. `ttl` must satisfy $1s \le \text{ttl} \le 3600s$.
3. Receipts must be consumed atomically inside an `EXCLUSIVE` SQLite transaction.
4. Attempting to consume an expired, already consumed, or revoked receipt returns a typed validation error.
