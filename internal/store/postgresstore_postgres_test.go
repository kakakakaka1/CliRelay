package store

import (
	"context"
	"os"
	"testing"
	"time"
)

func TestPostgresStoreMigratesLegacyAuthIDs(t *testing.T) {
	dsn := os.Getenv("CLIRELAY_POSTGRES_TEST_DSN")
	if dsn == "" {
		t.Skip("CLIRELAY_POSTGRES_TEST_DSN is not set")
	}
	ctx := context.Background()
	schema := "auth_store_test_" + time.Now().UTC().Format("20060102150405")
	store, err := NewPostgresStore(ctx, PostgresStoreConfig{DSN: dsn, Schema: schema, SpoolDir: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	defer func() { _, _ = store.db.ExecContext(ctx, `DROP SCHEMA IF EXISTS "`+schema+`" CASCADE`) }()

	if err = store.EnsureSchema(ctx); err != nil {
		t.Fatal(err)
	}
	authTable := store.fullTableName(store.cfg.AuthTable)
	if _, err = store.db.ExecContext(ctx, `INSERT INTO `+authTable+` (id, content) VALUES ($1, $2::jsonb)`, "legacy.json", `{"type":"codex"}`); err != nil {
		t.Fatal(err)
	}
	if err = store.EnsureSchema(ctx); err != nil {
		t.Fatal(err)
	}

	const wantID = "00000000-0000-0000-0000-000000000001/legacy.json"
	var id, tenantID string
	if err = store.db.QueryRowContext(ctx, `SELECT id, tenant_id FROM `+authTable).Scan(&id, &tenantID); err != nil {
		t.Fatal(err)
	}
	if id != wantID || tenantID != "00000000-0000-0000-0000-000000000001" {
		t.Fatalf("migrated auth row id=%q tenant=%q", id, tenantID)
	}
	items, err := store.List(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(items) != 1 || items[0].ID != wantID || items[0].TenantID != tenantID {
		t.Fatalf("listed auth rows = %#v", items)
	}
}
