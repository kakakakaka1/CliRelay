package usage

import (
	"database/sql"
	"fmt"
	"strings"
	"time"
)

const (
	rollupBucketMinute   = "minute"
	rollupBucketHour     = "hour"
	rollupBucketDay      = "day"
	rollupBucketLifetime = "lifetime"
	rollupLifetimeStart  = "1970-01-01"
	usageRollupSchemaSQL = `
CREATE TABLE IF NOT EXISTS usage_rollup_buckets (
  tenant_id               TEXT NOT NULL DEFAULT '00000000-0000-0000-0000-000000000001',
  bucket_kind             TEXT NOT NULL,
  bucket_start            TEXT NOT NULL,
  api_key_id              TEXT NOT NULL DEFAULT '',
  end_user_id             TEXT NOT NULL DEFAULT '',
  auth_subject_id         TEXT NOT NULL DEFAULT '',
  model                   TEXT NOT NULL DEFAULT '',
  source                  TEXT NOT NULL DEFAULT '',
  channel_name            TEXT NOT NULL DEFAULT '',
  request_count           INTEGER NOT NULL DEFAULT 0,
  success_count           INTEGER NOT NULL DEFAULT 0,
  failure_count           INTEGER NOT NULL DEFAULT 0,
  streaming_count         INTEGER NOT NULL DEFAULT 0,
  input_tokens            INTEGER NOT NULL DEFAULT 0,
  output_tokens           INTEGER NOT NULL DEFAULT 0,
  reasoning_tokens        INTEGER NOT NULL DEFAULT 0,
  cached_tokens           INTEGER NOT NULL DEFAULT 0,
  effective_input_tokens  INTEGER NOT NULL DEFAULT 0,
  total_tokens            INTEGER NOT NULL DEFAULT 0,
  cost_total              REAL NOT NULL DEFAULT 0,
  latency_sum_ms          INTEGER NOT NULL DEFAULT 0,
  latency_count           INTEGER NOT NULL DEFAULT 0,
  first_token_sum_ms      INTEGER NOT NULL DEFAULT 0,
  first_token_count       INTEGER NOT NULL DEFAULT 0,
  updated_at              DATETIME NOT NULL,
  PRIMARY KEY (
    tenant_id, bucket_kind, bucket_start,
    api_key_id, end_user_id, auth_subject_id,
    model, source, channel_name
  )
);
CREATE INDEX IF NOT EXISTS idx_usage_rollup_tenant_kind_start
  ON usage_rollup_buckets(tenant_id, bucket_kind, bucket_start);
CREATE INDEX IF NOT EXISTS idx_usage_rollup_tenant_key_day
  ON usage_rollup_buckets(tenant_id, bucket_kind, api_key_id, bucket_start);
CREATE INDEX IF NOT EXISTS idx_usage_rollup_tenant_user_day
  ON usage_rollup_buckets(tenant_id, bucket_kind, end_user_id, bucket_start);
CREATE INDEX IF NOT EXISTS idx_usage_rollup_tenant_subject_day
  ON usage_rollup_buckets(tenant_id, bucket_kind, auth_subject_id, bucket_start);
`
)

type rollupEvent struct {
	TenantID       string
	APIKeyID       string
	EndUserID      string
	AuthSubjectID  string
	Model          string
	Source         string
	ChannelName    string
	Failed         bool
	Streaming      bool
	LatencyMs      int64
	FirstTokenMs   int64
	Tokens         TokenStats
	Cost           float64
	At             time.Time
}

func ensureUsageRollupTables(db *sql.DB) error {
	if db == nil {
		return nil
	}
	if _, err := db.Exec(usageRollupSchemaSQL); err != nil {
		return fmt.Errorf("usage: create usage_rollup_buckets: %w", err)
	}
	return nil
}

func bootstrapUsageRollup(db *sql.DB) error {
	return ensureUsageRollupTables(db)
}

func rollupBucketStarts(at time.Time, loc *time.Location) map[string]string {
	if loc == nil {
		loc = time.Local
	}
	local := at.In(loc)
	return map[string]string{
		rollupBucketMinute:   local.Format("2006-01-02T15:04"),
		rollupBucketHour:     local.Format("2006-01-02T15"),
		rollupBucketDay:      local.Format("2006-01-02"),
		rollupBucketLifetime: rollupLifetimeStart,
	}
}

func projectUsageRollupTx(tx *sql.Tx, ev rollupEvent) error {
	if tx == nil {
		return nil
	}
	ev.TenantID = normalizeTenantID(ev.TenantID)
	ev.APIKeyID = strings.TrimSpace(ev.APIKeyID)
	ev.EndUserID = strings.TrimSpace(ev.EndUserID)
	ev.AuthSubjectID = strings.TrimSpace(ev.AuthSubjectID)
	ev.Model = strings.TrimSpace(ev.Model)
	ev.Source = strings.TrimSpace(ev.Source)
	ev.ChannelName = strings.TrimSpace(ev.ChannelName)
	if ev.At.IsZero() {
		ev.At = time.Now()
	}

	successInc, failureInc := int64(1), int64(0)
	if ev.Failed {
		successInc, failureInc = 0, 1
	}
	streamingInc := int64(0)
	if ev.Streaming {
		streamingInc = 1
	}
	effectiveInput := effectiveInputTokenTotal(ev.Tokens.InputTokens, ev.Tokens.CachedTokens)
	latencyCount := int64(0)
	if ev.LatencyMs > 0 {
		latencyCount = 1
	}
	firstTokenCount := int64(0)
	if ev.FirstTokenMs > 0 {
		firstTokenCount = 1
	}
	now := time.Now().UTC().Format(time.RFC3339Nano)
	starts := rollupBucketStarts(ev.At, getUsageLocation())

	const upsertSQL = `
		INSERT INTO usage_rollup_buckets (
			tenant_id, bucket_kind, bucket_start,
			api_key_id, end_user_id, auth_subject_id,
			model, source, channel_name,
			request_count, success_count, failure_count, streaming_count,
			input_tokens, output_tokens, reasoning_tokens, cached_tokens,
			effective_input_tokens, total_tokens, cost_total,
			latency_sum_ms, latency_count, first_token_sum_ms, first_token_count,
			updated_at
		) VALUES (
			?, ?, ?,
			?, ?, ?,
			?, ?, ?,
			1, ?, ?, ?,
			?, ?, ?, ?,
			?, ?, ?,
			?, ?, ?, ?,
			?
		)
		ON CONFLICT(
			tenant_id, bucket_kind, bucket_start,
			api_key_id, end_user_id, auth_subject_id,
			model, source, channel_name
		) DO UPDATE SET
			request_count = usage_rollup_buckets.request_count + 1,
			success_count = usage_rollup_buckets.success_count + excluded.success_count,
			failure_count = usage_rollup_buckets.failure_count + excluded.failure_count,
			streaming_count = usage_rollup_buckets.streaming_count + excluded.streaming_count,
			input_tokens = usage_rollup_buckets.input_tokens + excluded.input_tokens,
			output_tokens = usage_rollup_buckets.output_tokens + excluded.output_tokens,
			reasoning_tokens = usage_rollup_buckets.reasoning_tokens + excluded.reasoning_tokens,
			cached_tokens = usage_rollup_buckets.cached_tokens + excluded.cached_tokens,
			effective_input_tokens = usage_rollup_buckets.effective_input_tokens + excluded.effective_input_tokens,
			total_tokens = usage_rollup_buckets.total_tokens + excluded.total_tokens,
			cost_total = usage_rollup_buckets.cost_total + excluded.cost_total,
			latency_sum_ms = usage_rollup_buckets.latency_sum_ms + excluded.latency_sum_ms,
			latency_count = usage_rollup_buckets.latency_count + excluded.latency_count,
			first_token_sum_ms = usage_rollup_buckets.first_token_sum_ms + excluded.first_token_sum_ms,
			first_token_count = usage_rollup_buckets.first_token_count + excluded.first_token_count,
			updated_at = excluded.updated_at
	`

	for kind, start := range starts {
		if _, err := tx.Exec(upsertSQL,
			ev.TenantID, kind, start,
			ev.APIKeyID, ev.EndUserID, ev.AuthSubjectID,
			ev.Model, ev.Source, ev.ChannelName,
			successInc, failureInc, streamingInc,
			ev.Tokens.InputTokens, ev.Tokens.OutputTokens, ev.Tokens.ReasoningTokens, ev.Tokens.CachedTokens,
			effectiveInput, ev.Tokens.TotalTokens, ev.Cost,
			ev.LatencyMs, latencyCount, ev.FirstTokenMs, firstTokenCount,
			now,
		); err != nil {
			return fmt.Errorf("usage: project rollup %s: %w", kind, err)
		}
	}
	return nil
}

func resolveEndUserIDForKey(apiKey string) string {
	row := GetAPIKey(strings.TrimSpace(apiKey))
	if row == nil {
		return ""
	}
	return strings.TrimSpace(row.EndUserID)
}

// commitLogWithProjections writes AI-account daily + generic rollup then commits.
func commitLogWithProjections(tx *sql.Tx, ev rollupEvent) error {
	if err := projectUsageRollupTx(tx, ev); err != nil {
		_ = tx.Rollback()
		return err
	}
	if ev.AuthSubjectID != "" {
		if err := projectAuthSubjectUsageDailyTx(tx, ev.TenantID, ev.AuthSubjectID, ev.Failed, ev.Cost, ev.At); err != nil {
			_ = tx.Rollback()
			return fmt.Errorf("project auth subject usage daily: %w", err)
		}
	}
	if err := tx.Commit(); err != nil {
		return err
	}
	return nil
}
