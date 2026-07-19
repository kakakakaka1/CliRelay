package usage

import (
	"database/sql"
	"fmt"
	"strings"
	"time"
)

const endUserDailySpendingResetsTableSQL = `
CREATE TABLE IF NOT EXISTS end_user_daily_spending_resets (
  tenant_id     TEXT NOT NULL DEFAULT '00000000-0000-0000-0000-000000000001',
  end_user_id   TEXT NOT NULL,
  day_key       TEXT NOT NULL DEFAULT '',
  cost_baseline REAL NOT NULL DEFAULT 0,
  reset_at      TIMESTAMP NOT NULL,
  PRIMARY KEY (tenant_id, end_user_id)
);
CREATE INDEX IF NOT EXISTS idx_end_user_daily_spending_resets_day
  ON end_user_daily_spending_resets(tenant_id, day_key);
`

func bootstrapEndUserDailySpendingResets(db *sql.DB) error {
	if db == nil {
		return nil
	}
	if _, err := db.Exec(endUserDailySpendingResetsTableSQL); err != nil {
		return fmt.Errorf("usage: ensure end_user_daily_spending_resets: %w", err)
	}
	return nil
}

func QueryRawTodayCostByEndUserForTenant(tenantID, endUserID string) (float64, error) {
	db := getReadDB()
	if db == nil {
		return 0, nil
	}
	tenantID = normalizeTenantID(tenantID)
	endUserID = strings.TrimSpace(endUserID)
	predicate, args := buildEndUserAPIKeySelectorPredicate(tenantID, endUserID)
	queryArgs := append([]interface{}{tenantID, CutoffStartUTC(1).Format(time.RFC3339)}, args...)
	var total float64
	if err := db.QueryRow(
		"SELECT COALESCE(SUM(cost), 0) FROM request_logs WHERE tenant_id = ? AND timestamp >= ? AND "+predicate,
		queryArgs...,
	).Scan(&total); err != nil {
		return 0, fmt.Errorf("usage: query raw today cost by end user: %w", err)
	}
	return total, nil
}

func getEndUserDailySpendingResetBaseline(tenantID, endUserID string) (float64, bool, error) {
	db := getReadDB()
	if db == nil {
		return 0, false, nil
	}
	tenantID = normalizeTenantID(tenantID)
	endUserID = strings.TrimSpace(endUserID)
	var dayKey string
	var baseline float64
	err := db.QueryRow(
		`SELECT day_key, cost_baseline FROM end_user_daily_spending_resets WHERE tenant_id = ? AND end_user_id = ?`,
		tenantID, endUserID,
	).Scan(&dayKey, &baseline)
	if err == sql.ErrNoRows {
		return 0, false, nil
	}
	if err != nil {
		return 0, false, fmt.Errorf("usage: get end-user daily spending baseline: %w", err)
	}
	if dayKey != LocalDayKeyAt(time.Now()) {
		return 0, false, nil
	}
	return baseline, true, nil
}

func QueryTodayEffectiveCostByEndUserForTenant(tenantID, endUserID string) (float64, error) {
	raw, err := QueryRawTodayCostByEndUserForTenant(tenantID, endUserID)
	if err != nil {
		return 0, err
	}
	baseline, ok, err := getEndUserDailySpendingResetBaseline(tenantID, endUserID)
	if err != nil || !ok {
		return raw, err
	}
	return effectiveTodayCost(raw, baseline), nil
}

// ResetTodayCostByEndUser sets an account-level baseline without deleting logs.
func ResetTodayCostByEndUser(tenantID, endUserID string) (usedBefore float64, rawToday float64, err error) {
	db := getDB()
	if db == nil {
		return 0, 0, nil
	}
	tenantID = normalizeTenantID(tenantID)
	endUserID = strings.TrimSpace(endUserID)
	usedBefore, err = QueryTodayEffectiveCostByEndUserForTenant(tenantID, endUserID)
	if err != nil {
		return 0, 0, err
	}
	rawToday, err = QueryRawTodayCostByEndUserForTenant(tenantID, endUserID)
	if err != nil {
		return 0, 0, err
	}
	now := time.Now().UTC()
	_, err = db.Exec(`
		INSERT INTO end_user_daily_spending_resets (tenant_id, end_user_id, day_key, cost_baseline, reset_at)
		VALUES (?, ?, ?, ?, ?)
		ON CONFLICT (tenant_id, end_user_id) DO UPDATE SET
			day_key = excluded.day_key,
			cost_baseline = excluded.cost_baseline,
			reset_at = excluded.reset_at
	`, tenantID, endUserID, LocalDayKeyAt(now), rawToday, now.Format(time.RFC3339Nano))
	if err != nil {
		return 0, 0, fmt.Errorf("usage: reset today cost by end user: %w", err)
	}
	return usedBefore, rawToday, nil
}
