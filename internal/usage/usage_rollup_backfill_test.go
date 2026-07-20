package usage

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/router-for-me/CLIProxyAPI/v6/internal/config"
)

func TestUsageRollupBackfillResolvesLegacyKeyAndEndUser(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "rollup-backfill.db")
	// Init without marker so we control order: insert legacy rows first via raw SQL
	// after Init (marker already done on empty). Force rebuild by clearing marker.
	if err := InitDB(dbPath, config.RequestLogStorageConfig{RetentionDays: 7}, time.UTC); err != nil {
		t.Fatalf("InitDB: %v", err)
	}
	t.Cleanup(func() {
		stopRequestLogMaintenance()
		CloseDB()
	})

	endUserID := "eu-backfill-1"
	if err := UpsertAPIKey(APIKeyRow{
		ID: "key-bf-1", Key: "sk-bf-legacy", Name: "BF", EndUserID: endUserID,
		CreatedAt: time.Now().UTC().Format(time.RFC3339), UpdatedAt: time.Now().UTC().Format(time.RFC3339),
	}); err != nil {
		t.Fatalf("UpsertAPIKey: %v", err)
	}

	// Legacy detail: empty api_key_id, raw secret only.
	at := time.Date(2026, 7, 20, 10, 0, 0, 0, time.UTC)
	if _, err := getDB().Exec(`
		INSERT INTO request_logs
		(tenant_id, timestamp, api_key, api_key_id, model, source, failed, total_tokens, cost)
		VALUES (?, ?, ?, '', 'm', 's', 0, 10, 1.5)
	`, systemTenantID, at.Format(time.RFC3339Nano), "sk-bf-legacy"); err != nil {
		t.Fatalf("insert legacy: %v", err)
	}

	// Simulate incomplete first backfill then repair: clear marker + rollup and re-run after key exists.
	if _, err := getDB().Exec(`DELETE FROM usage_rollup_buckets`); err != nil {
		t.Fatalf("clear rollup: %v", err)
	}
	if _, err := getDB().Exec(`DELETE FROM usage_projection_markers WHERE marker_key = ?`, usageRollupBackfillMarker); err != nil {
		t.Fatalf("clear marker: %v", err)
	}
	// Repair api_key_id like production backfill does.
	if _, err := getDB().Exec(`UPDATE request_logs SET api_key_id = 'key-bf-1' WHERE api_key = 'sk-bf-legacy'`); err != nil {
		t.Fatalf("repair key id: %v", err)
	}
	if err := runUsageRollupBackfillAtInitDB(getDB(), time.UTC); err != nil {
		t.Fatalf("backfill: %v", err)
	}

	var keyTotal, userTotal float64
	if err := getDB().QueryRow(`
		SELECT COALESCE(SUM(cost_total),0) FROM usage_rollup_buckets
		WHERE bucket_kind='lifetime' AND api_key_id='key-bf-1'
	`).Scan(&keyTotal); err != nil {
		t.Fatalf("query key: %v", err)
	}
	if keyTotal != 1.5 {
		t.Fatalf("keyed rollup total = %v, want 1.5 after legacy identity repair", keyTotal)
	}
	if err := getDB().QueryRow(`
		SELECT COALESCE(SUM(cost_total),0) FROM usage_rollup_buckets
		WHERE bucket_kind='lifetime' AND end_user_id=?
	`, endUserID).Scan(&userTotal); err != nil {
		t.Fatalf("query user: %v", err)
	}
	if userTotal != 1.5 {
		t.Fatalf("end-user rollup total = %v, want 1.5", userTotal)
	}
}

func TestUsageRollupBackfillIsReentrantWithoutDoubleCount(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "rollup-reentrant.db")
	if err := InitDB(dbPath, config.RequestLogStorageConfig{RetentionDays: 7}, time.UTC); err != nil {
		t.Fatalf("InitDB: %v", err)
	}
	t.Cleanup(func() {
		stopRequestLogMaintenance()
		CloseDB()
	})
	if err := UpsertAPIKey(APIKeyRow{ID: "key-r1", Key: "sk-r1"}); err != nil {
		t.Fatalf("UpsertAPIKey: %v", err)
	}
	at := time.Date(2026, 7, 20, 11, 0, 0, 0, time.UTC)
	if _, err := getDB().Exec(`
		INSERT INTO request_logs
		(tenant_id, timestamp, api_key, api_key_id, model, source, failed, total_tokens, cost)
		VALUES (?, ?, 'sk-r1', 'key-r1', 'm', 's', 0, 1, 2)
	`, systemTenantID, at.Format(time.RFC3339Nano)); err != nil {
		t.Fatalf("insert: %v", err)
	}

	// Force rebuild twice with marker cleared between — absolute rebuild must not double.
	for i := 0; i < 2; i++ {
		if _, err := getDB().Exec(`DELETE FROM usage_projection_markers WHERE marker_key = ?`, usageRollupBackfillMarker); err != nil {
			t.Fatalf("clear marker: %v", err)
		}
		if err := runUsageRollupBackfillAtInitDB(getDB(), time.UTC); err != nil {
			t.Fatalf("backfill %d: %v", i, err)
		}
	}
	var total float64
	if err := getDB().QueryRow(`
		SELECT COALESCE(SUM(cost_total),0) FROM usage_rollup_buckets
		WHERE bucket_kind='lifetime' AND api_key_id='key-r1'
	`).Scan(&total); err != nil {
		t.Fatalf("query: %v", err)
	}
	if total != 2 {
		t.Fatalf("lifetime rollup total after replay = %v, want 2 (not doubled)", total)
	}
}

func TestUsageRollupBackfillDoesNotMarkDoneOnReadFailure(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "rollup-fail.db")
	if err := InitDB(dbPath, config.RequestLogStorageConfig{RetentionDays: 7}, time.UTC); err != nil {
		t.Fatalf("InitDB: %v", err)
	}
	t.Cleanup(func() {
		stopRequestLogMaintenance()
		CloseDB()
	})
	// Drop request_logs to force query failure after marker cleared.
	if _, err := getDB().Exec(`DROP TABLE request_logs`); err != nil {
		t.Fatalf("drop: %v", err)
	}
	if _, err := getDB().Exec(`DELETE FROM usage_projection_markers WHERE marker_key = ?`, usageRollupBackfillMarker); err != nil {
		t.Fatalf("clear marker: %v", err)
	}
	if err := runUsageRollupBackfillAtInitDB(getDB(), time.UTC); err == nil {
		t.Fatal("expected backfill error when request_logs missing")
	}
	if projectionMarkerValue(getDB(), usageRollupBackfillMarker) == "done" {
		t.Fatal("marker must not be done after failed backfill")
	}
}

func TestUsageRollupRetentionRunsWhenDetailCleanupDisabled(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "rollup-cleanup-flag.db")
	disabled := false
	if err := InitDB(dbPath, config.RequestLogStorageConfig{
		RetentionDays:  7,
		CleanupEnabled: &disabled,
	}, time.UTC); err != nil {
		t.Fatalf("InitDB: %v", err)
	}
	t.Cleanup(func() {
		stopRequestLogMaintenance()
		CloseDB()
	})
	now := time.Now().UTC()
	if _, err := getDB().Exec(`
		INSERT INTO usage_rollup_buckets (
			tenant_id, bucket_kind, bucket_start, api_key_id, end_user_id, auth_subject_id,
			model, source, channel_name, request_count, success_count, failure_count, streaming_count,
			input_tokens, output_tokens, reasoning_tokens, cached_tokens, effective_input_tokens,
			total_tokens, cost_total, latency_sum_ms, latency_count, first_token_sum_ms, first_token_count, updated_at
		) VALUES (?, 'minute', ?, 'k', '', '', 'm', 's', '', 1, 1, 0, 0, 0, 0, 0, 0, 0, 1, 1, 0, 0, 0, 0, ?)
	`, systemTenantID, now.Add(-48*time.Hour).Format("2006-01-02T15:04"), now.Format(time.RFC3339Nano)); err != nil {
		t.Fatalf("insert: %v", err)
	}
	// Simulate maintenance pass with detail cleanup disabled.
	runRequestLogMaintenancePass(context.Background(), getDB(), "sqlite")
	var n int64
	if err := getDB().QueryRow(`SELECT COUNT(*) FROM usage_rollup_buckets WHERE bucket_kind='minute'`).Scan(&n); err != nil {
		t.Fatalf("count: %v", err)
	}
	if n != 0 {
		t.Fatalf("stale minute buckets remaining = %d, want 0 even when cleanup-enabled=false", n)
	}
}

func TestUsageRollupBucketRetentionPrunesMinuteHourDay(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "rollup-retention.db")
	if err := InitDB(dbPath, config.RequestLogStorageConfig{RetentionDays: 7}, time.UTC); err != nil {
		t.Fatalf("InitDB: %v", err)
	}
	t.Cleanup(func() {
		stopRequestLogMaintenance()
		CloseDB()
	})
	now := time.Now().UTC()
	// Insert stale minute/hour/day and a lifetime row.
	for _, row := range []struct {
		kind, start string
	}{
		{rollupBucketMinute, now.Add(-48 * time.Hour).Format("2006-01-02T15:04")},
		{rollupBucketHour, now.Add(-40 * 24 * time.Hour).Format("2006-01-02T15")},
		{rollupBucketDay, now.Add(-500 * 24 * time.Hour).Format("2006-01-02")},
		{rollupBucketLifetime, rollupLifetimeStart},
	} {
		if _, err := getDB().Exec(`
			INSERT INTO usage_rollup_buckets (
				tenant_id, bucket_kind, bucket_start, api_key_id, end_user_id, auth_subject_id,
				model, source, channel_name, request_count, success_count, failure_count, streaming_count,
				input_tokens, output_tokens, reasoning_tokens, cached_tokens, effective_input_tokens,
				total_tokens, cost_total, latency_sum_ms, latency_count, first_token_sum_ms, first_token_count, updated_at
			) VALUES (?, ?, ?, 'k', '', '', 'm', 's', '', 1, 1, 0, 0, 0, 0, 0, 0, 0, 1, 1, 0, 0, 0, 0, ?)
		`, systemTenantID, row.kind, row.start, now.Format(time.RFC3339Nano)); err != nil {
			t.Fatalf("insert %s: %v", row.kind, err)
		}
	}
	n, err := cleanupExpiredUsageRollupBuckets(getDB())
	if err != nil {
		t.Fatalf("cleanup: %v", err)
	}
	if n < 3 {
		t.Fatalf("pruned = %d, want at least 3", n)
	}
	var life int64
	if err := getDB().QueryRow(`SELECT COUNT(*) FROM usage_rollup_buckets WHERE bucket_kind='lifetime'`).Scan(&life); err != nil {
		t.Fatalf("count lifetime: %v", err)
	}
	if life != 1 {
		t.Fatalf("lifetime rows = %d, want 1", life)
	}
}
