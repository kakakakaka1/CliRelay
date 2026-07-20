package enduser

import (
	"context"
	"database/sql"
	"errors"
	"strings"
	"testing"

	"github.com/google/uuid"
	sqlapikey "github.com/router-for-me/CLIProxyAPI/v6/internal/storage/sqlite/apikey"
	_ "modernc.org/sqlite"
)

func openEndUserTestDB(t *testing.T) *sql.DB {
	t.Helper()
	db, err := sql.Open("sqlite", "file:"+t.Name()+"?mode=memory&cache=shared")
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	sqlapikey.InitTable(db)
	if _, err := db.Exec(`
		CREATE TABLE IF NOT EXISTS end_users (
			id TEXT PRIMARY KEY,
			tenant_id TEXT NOT NULL,
			username TEXT NOT NULL,
			username_normalized TEXT NOT NULL UNIQUE,
			display_name TEXT NOT NULL,
			password_hash TEXT NOT NULL,
			status TEXT NOT NULL DEFAULT 'active',
			must_change_password INTEGER NOT NULL DEFAULT 0,
			password_changed_at TEXT,
			last_login_at TEXT,
			failed_login_count INTEGER NOT NULL DEFAULT 0,
			lock_stage INTEGER NOT NULL DEFAULT 0,
			locked_until TEXT,
			created_at TEXT NOT NULL DEFAULT '',
			updated_at TEXT NOT NULL DEFAULT '',
			version INTEGER NOT NULL DEFAULT 1,
			permission_profile_id TEXT NOT NULL DEFAULT '',
			daily_limit INTEGER NOT NULL DEFAULT 0,
			total_quota INTEGER NOT NULL DEFAULT 0,
			spending_limit REAL NOT NULL DEFAULT 0,
			daily_spending_limit REAL NOT NULL DEFAULT 0,
			concurrency_limit INTEGER NOT NULL DEFAULT 0,
			rpm_limit INTEGER NOT NULL DEFAULT 0,
			tpm_limit INTEGER NOT NULL DEFAULT 0,
			allowed_models TEXT NOT NULL DEFAULT '[]',
			allowed_channels TEXT NOT NULL DEFAULT '[]',
			allowed_channel_groups TEXT NOT NULL DEFAULT '[]',
			system_prompt TEXT NOT NULL DEFAULT ''
		)
	`); err != nil {
		t.Fatalf("create end_users: %v", err)
	}
	return db
}

func TestDeleteKeyPromotesDefaultOnSQLite(t *testing.T) {
	t.Parallel()
	db := openEndUserTestDB(t)
	svc := NewService(db)
	ctx := context.Background()
	tenantID := "00000000-0000-0000-0000-000000000001"
	userID := uuid.NewString()
	if _, err := db.Exec(`
		INSERT INTO end_users (id, tenant_id, username, username_normalized, display_name, password_hash, status, created_at, updated_at)
		VALUES (?, ?, 'alice', 'alice', 'Alice', 'x', 'active', '2026-01-01T00:00:00Z', '2026-01-01T00:00:00Z')
	`, userID, tenantID); err != nil {
		t.Fatalf("insert user: %v", err)
	}

	first, err := svc.CreateKey(ctx, tenantID, userID, "first")
	if err != nil {
		t.Fatalf("create first: %v", err)
	}
	second, err := svc.CreateKey(ctx, tenantID, userID, "second")
	if err != nil {
		t.Fatalf("create second: %v", err)
	}
	if !first.APIKey.IsDefault {
		t.Fatal("first key should be default")
	}
	if second.APIKey.IsDefault {
		t.Fatal("second key should not be default")
	}

	if err := svc.DeleteKey(ctx, tenantID, userID, first.APIKey.ID); err != nil {
		t.Fatalf("delete default key: %v", err)
	}
	keys, err := svc.ListKeys(ctx, tenantID, userID)
	if err != nil {
		t.Fatalf("list keys: %v", err)
	}
	if len(keys) != 1 {
		t.Fatalf("keys after delete = %d, want 1", len(keys))
	}
	if !keys[0].IsDefault {
		t.Fatal("remaining key must be promoted to default")
	}
	var disabled int
	var storedSecret string
	if err := db.QueryRow(`SELECT disabled, key FROM api_keys WHERE tenant_id = ? AND id = ?`, tenantID, first.APIKey.ID).Scan(&disabled, &storedSecret); err != nil {
		t.Fatalf("query soft-deleted key: %v", err)
	}
	if disabled != 1 {
		t.Fatalf("disabled = %d, want 1", disabled)
	}
	if storedSecret == first.PlaintextKey || !strings.HasPrefix(storedSecret, "sk-deleted-") {
		t.Fatalf("soft-deleted secret not invalidated: %q", storedSecret)
	}
	if err := svc.DeleteKey(ctx, tenantID, userID, keys[0].ID); !errors.Is(err, ErrLastKey) {
		t.Fatalf("delete last key err = %v, want ErrLastKey", err)
	}
}

func TestSetDefaultKeyOnSQLite(t *testing.T) {
	t.Parallel()
	db := openEndUserTestDB(t)
	svc := NewService(db)
	ctx := context.Background()
	tenantID := "00000000-0000-0000-0000-000000000001"
	userID := uuid.NewString()
	if _, err := db.Exec(`
		INSERT INTO end_users (id, tenant_id, username, username_normalized, display_name, password_hash, status, created_at, updated_at)
		VALUES (?, ?, 'bob', 'bob', 'Bob', 'x', 'active', '2026-01-01T00:00:00Z', '2026-01-01T00:00:00Z')
	`, userID, tenantID); err != nil {
		t.Fatalf("insert user: %v", err)
	}
	a, err := svc.CreateKey(ctx, tenantID, userID, "a")
	if err != nil {
		t.Fatalf("create a: %v", err)
	}
	b, err := svc.CreateKey(ctx, tenantID, userID, "b")
	if err != nil {
		t.Fatalf("create b: %v", err)
	}
	if _, err := svc.CreateKey(ctx, tenantID, userID, "A"); !errors.Is(err, ErrDuplicateKeyName) {
		t.Fatalf("duplicate name err = %v, want ErrDuplicateKeyName", err)
	}
	if err := svc.UpdateKeyName(ctx, tenantID, userID, b.APIKey.ID, "a"); !errors.Is(err, ErrDuplicateKeyName) {
		t.Fatalf("rename to duplicate err = %v, want ErrDuplicateKeyName", err)
	}
	if err := svc.UpdateKeyName(ctx, tenantID, userID, a.APIKey.ID, "a"); err != nil {
		t.Fatalf("keep same name: %v", err)
	}
	if err := svc.SetDefaultKey(ctx, tenantID, userID, b.APIKey.ID); err != nil {
		t.Fatalf("set default: %v", err)
	}
	keys, err := svc.ListKeys(ctx, tenantID, userID)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	var defaultCount int
	for _, k := range keys {
		if k.IsDefault {
			defaultCount++
			if k.ID != b.APIKey.ID {
				t.Fatalf("default key id = %s, want %s", k.ID, b.APIKey.ID)
			}
		}
	}
	if defaultCount != 1 {
		t.Fatalf("default count = %d, want 1", defaultCount)
	}
	_ = a
}
