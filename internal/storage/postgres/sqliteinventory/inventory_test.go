package sqliteinventory

import (
	"context"
	"database/sql"
	"path/filepath"
	"testing"
	"time"

	_ "modernc.org/sqlite"
)

func TestCollect(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "usage.db")
	db, err := sql.Open("sqlite", dbPath)
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	if _, err := db.Exec(`
		CREATE TABLE request_logs (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			timestamp DATETIME NOT NULL,
			api_key TEXT NOT NULL,
			total_tokens INTEGER NOT NULL
		);
		CREATE TABLE api_keys (
			key TEXT PRIMARY KEY,
			id TEXT NOT NULL,
			updated_at TEXT NOT NULL
		);
		INSERT INTO request_logs (timestamp, api_key, total_tokens)
		VALUES ('2026-07-05T01:00:00Z', 'fixture-key-a', 10),
		       ('2026-07-05T02:00:00Z', 'fixture-key-b', 20);
		INSERT INTO api_keys (key, id, updated_at)
		VALUES ('fixture-key-a', 'key-a', '2026-07-05T03:00:00Z');
	`); err != nil {
		t.Fatalf("seed sqlite: %v", err)
	}
	if err := db.Close(); err != nil {
		t.Fatalf("close sqlite: %v", err)
	}

	got, err := Collect(context.Background(), Options{
		Path: dbPath,
		Now:  time.Date(2026, 7, 5, 3, 4, 0, 0, time.UTC),
	})
	if err != nil {
		t.Fatalf("Collect() error = %v", err)
	}
	if len(got.Tables) != 2 {
		t.Fatalf("tables len = %d", len(got.Tables))
	}
	logs := findTable(got.Tables, "request_logs")
	if logs == nil {
		t.Fatal("request_logs inventory missing")
	}
	if logs.RowCount != 2 || logs.MinID == nil || *logs.MinID != 1 || logs.MaxID == nil || *logs.MaxID != 2 {
		t.Fatalf("request_logs stats = %#v", logs)
	}
	if logs.MinTime != "2026-07-05T01:00:00Z" || logs.MaxTime != "2026-07-05T02:00:00Z" {
		t.Fatalf("request_logs time range = %q %q", logs.MinTime, logs.MaxTime)
	}
	if logs.Checksum == "" || !logs.DryRunOnly {
		t.Fatalf("request_logs checksum/dry-run = %#v", logs)
	}
	keys := findTable(got.Tables, "api_keys")
	if keys == nil || keys.RowCount != 1 || keys.Checksum == "" {
		t.Fatalf("api_keys stats = %#v", keys)
	}
}

func findTable(rows []TableStats, name string) *TableStats {
	for i := range rows {
		if rows[i].Name == name {
			return &rows[i]
		}
	}
	return nil
}
