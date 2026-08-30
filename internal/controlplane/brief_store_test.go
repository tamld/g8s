package controlplane

import (
	"context"
	"database/sql"
	"strings"
	"testing"
	"time"
)

func TestBriefStoreLifecycle(t *testing.T) {
	now := time.Now()
	store := newTestControlPlane(t, func() time.Time { return now })
	ctx := context.Background()

	b := BriefRow{
		ID:        "brief-lifecycle-1",
		Title:     "DELTA-15 Orchestrator",
		PayloadMD: "# Task\nImplement worker.",
		DodMD:     "- [ ] All tests pass\n- [ ] Coverage >= 90%",
		IssuedBy:  "sisyphus",
		IssuedAt:  now,
		ExpiresAt: now.Add(2 * time.Hour),
		Status:    "active",
	}

	if err := store.CreateBrief(ctx, b); err != nil {
		t.Fatalf("create brief: %v", err)
	}

	got, err := store.GetBrief(ctx, "brief-lifecycle-1")
	if err != nil {
		t.Fatalf("get brief: %v", err)
	}
	if got.Title != b.Title {
		t.Errorf("title = %q, want %q", got.Title, b.Title)
	}
	if got.PayloadMD != b.PayloadMD {
		t.Errorf("payload_md = %q, want %q", got.PayloadMD, b.PayloadMD)
	}
	if got.DodMD != b.DodMD {
		t.Errorf("dod_md = %q, want %q", got.DodMD, b.DodMD)
	}
	if got.IssuedBy != b.IssuedBy {
		t.Errorf("issued_by = %q, want %q", got.IssuedBy, b.IssuedBy)
	}
	if got.Status != "active" {
		t.Errorf("status = %q, want active", got.Status)
	}

	// Update status
	if err := store.UpdateBriefStatus(ctx, "brief-lifecycle-1", "consumed"); err != nil {
		t.Fatalf("update status: %v", err)
	}

	gotUpdated, err := store.GetBrief(ctx, "brief-lifecycle-1")
	if err != nil {
		t.Fatalf("get updated: %v", err)
	}
	if gotUpdated.Status != "consumed" {
		t.Errorf("status after update = %q, want consumed", gotUpdated.Status)
	}

	// ListActive excludes consumed
	active, err := store.ListActiveBriefs(ctx)
	if err != nil {
		t.Fatalf("list active briefs: %v", err)
	}
	for _, ab := range active {
		if ab.ID == "brief-lifecycle-1" {
			t.Errorf("consumed brief %s should not appear in active list", ab.ID)
		}
	}
}

func TestBriefStoreValidationAndMissing(t *testing.T) {
	now := time.Now()
	store := newTestControlPlane(t, func() time.Time { return now })
	ctx := context.Background()

	// Missing fields in CreateBrief
	if err := store.CreateBrief(ctx, BriefRow{}); err == nil {
		t.Errorf("empty brief should fail")
	}
	if err := store.CreateBrief(ctx, BriefRow{ID: "b1"}); err == nil {
		t.Errorf("missing title should fail")
	}
	if err := store.CreateBrief(ctx, BriefRow{ID: "b1", Title: "t"}); err == nil {
		t.Errorf("missing payload_md should fail")
	}
	if err := store.CreateBrief(ctx, BriefRow{ID: "b1", Title: "t", PayloadMD: "p"}); err == nil {
		t.Errorf("missing dod_md should fail")
	}
	if err := store.CreateBrief(ctx, BriefRow{ID: "b1", Title: "t", PayloadMD: "p", DodMD: "d"}); err == nil {
		t.Errorf("missing issued_by should fail")
	}
	if err := store.CreateBrief(ctx, BriefRow{ID: "b1", Title: "t", PayloadMD: "p", DodMD: "d", IssuedBy: "i"}); err == nil {
		t.Errorf("missing expires_at should fail")
	}

	// Duplicate ID
	valid := BriefRow{
		ID:        "b-dup",
		Title:     "Title",
		PayloadMD: "Payload",
		DodMD:     "DoD",
		IssuedBy:  "sisyphus",
		ExpiresAt: now.Add(time.Hour),
	}
	if err := store.CreateBrief(ctx, valid); err != nil {
		t.Fatalf("create valid brief: %v", err)
	}
	if err := store.CreateBrief(ctx, valid); err == nil || !strings.Contains(err.Error(), "UNIQUE") {
		t.Errorf("duplicate ID want UNIQUE constraint error, got %v", err)
	}

	// Get missing
	if _, err := store.GetBrief(ctx, "nonexistent"); err == nil || !strings.Contains(err.Error(), "unknown brief") {
		t.Errorf("get missing want ErrUnknownBrief, got %v", err)
	}
	if _, err := store.GetBrief(ctx, ""); err == nil {
		t.Errorf("get empty id should fail")
	}

	// Update missing
	if err := store.UpdateBriefStatus(ctx, "nonexistent", "consumed"); err == nil || !strings.Contains(err.Error(), "unknown brief") {
		t.Errorf("update missing want ErrUnknownBrief, got %v", err)
	}
	if err := store.UpdateBriefStatus(ctx, "", "consumed"); err == nil {
		t.Errorf("update empty id should fail")
	}
	if err := store.UpdateBriefStatus(ctx, "b-dup", ""); err == nil {
		t.Errorf("update empty status should fail")
	}
}

func TestBriefStoreMigrationFromV5(t *testing.T) {
	store, path := newTestStore(t)
	store.Close()

	// Simulate v5 schema: drop briefs table and set PRAGMA user_version = 5
	raw := openRawDB(t, path)
	_, _ = raw.Exec("DROP TABLE IF EXISTS briefs")
	if _, err := raw.Exec("PRAGMA user_version = 5"); err != nil {
		t.Fatalf("set user_version = 5: %v", err)
	}
	raw.Close()

	// Reopen with NewControlPlane, migrating to v6
	migratedStore, err := NewControlPlane(path, nil)
	if err != nil {
		t.Fatalf("open and migrate v5 db: %v", err)
	}
	defer migratedStore.Close()

	check := openRawDB(t, path)
	var version int
	if err := check.QueryRow("PRAGMA user_version").Scan(&version); err != nil {
		t.Fatalf("read user_version: %v", err)
	}
	if version != SchemaVersion {
		t.Errorf("user_version = %d, want %d", version, SchemaVersion)
	}

	// Verify briefs table exists and has all columns
	colRows, err := check.Query("PRAGMA table_info(briefs)")
	if err != nil {
		t.Fatalf("table_info(briefs): %v", err)
	}
	defer colRows.Close()
	cols := map[string]string{}
	for colRows.Next() {
		var cid int
		var name, colType string
		var notNull int
		var dflt sql.NullString
		var pk int
		if err := colRows.Scan(&cid, &name, &colType, &notNull, &dflt, &pk); err != nil {
			t.Fatalf("scan table_info: %v", err)
		}
		cols[name] = colType
	}
	for _, col := range []string{"id", "title", "payload_md", "dod_md", "issued_by", "issued_at", "expires_at", "status"} {
		if _, ok := cols[col]; !ok {
			t.Errorf("missing column %q in briefs table", col)
		}
	}
}

func TestBriefStoreMigrationIdempotent(t *testing.T) {
	store, path := newTestStore(t)
	defer store.Close()

	raw := openRawDB(t, path)
	conn, err := raw.Conn(context.Background())
	if err != nil {
		t.Fatalf("acquire raw connection: %v", err)
	}
	defer conn.Close()

	if err := applyBriefsSchema(conn); err != nil {
		t.Fatalf("applyBriefsSchema rerun failed: %v", err)
	}
	if err := migrateBriefsSchema(conn); err != nil {
		t.Fatalf("migrateBriefsSchema rerun failed: %v", err)
	}
}
