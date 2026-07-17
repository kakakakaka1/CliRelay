package middleware

import (
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/router-for-me/CLIProxyAPI/v6/internal/diagnostics"
	log "github.com/sirupsen/logrus"
)

// ─── Sliding window counters ────────────────────────────────────────────────

const windowDuration = 60 * time.Second

// slidingWindow tracks timestamped events within the last 60 seconds.
type slidingWindow struct {
	mu     sync.Mutex
	events []time.Time
}

func (w *slidingWindow) add() {
	now := time.Now()
	w.mu.Lock()
	w.events = append(w.events, now)
	w.mu.Unlock()
}

func (w *slidingWindow) count() int {
	cutoff := time.Now().Add(-windowDuration)
	w.mu.Lock()
	defer w.mu.Unlock()
	// Trim old events
	i := 0
	for i < len(w.events) && w.events[i].Before(cutoff) {
		i++
	}
	if i > 0 {
		w.events = w.events[i:]
	}
	return len(w.events)
}

// tokenWindow tracks timestamped token counts within the last 60 seconds.
type tokenWindow struct {
	mu      sync.Mutex
	entries []tokenEntry
	total   atomic.Int64 // cached total for fast reads
}

type tokenEntry struct {
	ts     time.Time
	tokens int64
}

func (w *tokenWindow) add(tokens int64) {
	now := time.Now()
	w.mu.Lock()
	w.entries = append(w.entries, tokenEntry{ts: now, tokens: tokens})
	w.mu.Unlock()
	w.total.Add(tokens)
}

func (w *tokenWindow) sum() int64 {
	cutoff := time.Now().Add(-windowDuration)
	w.mu.Lock()
	defer w.mu.Unlock()
	// Trim old entries and recalculate
	i := 0
	var expired int64
	for i < len(w.entries) && w.entries[i].ts.Before(cutoff) {
		expired += w.entries[i].tokens
		i++
	}
	if i > 0 {
		w.entries = w.entries[i:]
		w.total.Add(-expired)
	}
	return w.total.Load()
}

// ─── Per-key tracker registry ───────────────────────────────────────────────

var (
	rpmTrackers sync.Map // map[string]*slidingWindow
	tpmTrackers sync.Map // map[string]*tokenWindow

	inFlightMu    sync.Mutex
	inFlightByKey = map[string]int{}
)

func getRPMTracker(apiKey string) *slidingWindow {
	if v, ok := rpmTrackers.Load(apiKey); ok {
		return v.(*slidingWindow)
	}
	w := &slidingWindow{}
	actual, _ := rpmTrackers.LoadOrStore(apiKey, w)
	return actual.(*slidingWindow)
}

func getTPMTracker(apiKey string) *tokenWindow {
	if v, ok := tpmTrackers.Load(apiKey); ok {
		return v.(*tokenWindow)
	}
	w := &tokenWindow{}
	actual, _ := tpmTrackers.LoadOrStore(apiKey, w)
	return actual.(*tokenWindow)
}

// RecordTokenUsage records token consumption for TPM tracking.
// This should be called by the usage reporter after a request completes.
func RecordTokenUsage(apiKey string, totalTokens int64) {
	if apiKey == "" || totalTokens <= 0 {
		return
	}
	getTPMTracker(apiKey).add(totalTokens)
}

// ─── Quota Middleware ───────────────────────────────────────────────────────

// QuotaMiddleware enforces daily-limit, total-quota, RPM (requests per minute),
// TPM (tokens per minute), and spending restrictions for authenticated API keys.
//
// It reads the limits from the accessMetadata set by the auth provider.
// This middleware MUST be placed after AuthMiddleware and before route handlers.
// Only POST requests are checked (GET /models etc. don't consume quota).
func QuotaMiddleware() gin.HandlerFunc {
	return func(c *gin.Context) {
		// Only enforce on POST requests (actual API calls)
		if c.Request.Method != http.MethodPost {
			c.Next()
			return
		}

		// Get the authenticated API key
		apiKeyVal, exists := c.Get("apiKey")
		if !exists {
			c.Next()
			return
		}
		apiKey, ok := apiKeyVal.(string)
		if !ok || apiKey == "" {
			c.Next()
			return
		}

		// ── Always record this request for system-wide RPM tracking ──
		// This must happen before any metadata checks so ALL authenticated
		// POST requests are counted for the dashboard RPM display.
		rpmTracker := getRPMTracker(apiKey)
		rpmTracker.add()

		// Get access metadata containing limits
		metadataVal, exists := c.Get("accessMetadata")
		if !exists {
			c.Next()
			return
		}
		metadata, ok := metadataVal.(map[string]string)
		if !ok {
			c.Next()
			return
		}

		// Parse limits from metadata
		dailyLimit := parseIntMetadata(metadata, "daily-limit")
		totalQuota := parseIntMetadata(metadata, "total-quota")
		concurrencyLimit := parseIntMetadata(metadata, "concurrency-limit")
		rpmLimit := parseIntMetadata(metadata, "rpm-limit")
		tpmLimit := parseIntMetadata(metadata, "tpm-limit")
		spendingLimit := parseFloatMetadata(metadata, "spending-limit")
		dailySpendingLimit := parseFloatMetadata(metadata, "daily-spending-limit")
		diagnostics.SetQuotaLimits(c, diagnostics.QuotaSnapshot{
			DailyLimit:         dailyLimit,
			TotalQuota:         totalQuota,
			ConcurrencyLimit:   concurrencyLimit,
			RPMLimit:           rpmLimit,
			TPMLimit:           tpmLimit,
			SpendingLimit:      spendingLimit,
			DailySpendingLimit: dailySpendingLimit,
		})

		// Cache limits for dashboard snapshot
		UpdateKeyLimits(apiKey, rpmLimit, tpmLimit)

		// No limits configured — skip all checks
		if dailyLimit <= 0 && totalQuota <= 0 && concurrencyLimit <= 0 && rpmLimit <= 0 && tpmLimit <= 0 && spendingLimit <= 0 && dailySpendingLimit <= 0 {
			c.Next()
			return
		}

		if concurrencyLimit > 0 {
			release, ok := acquireKeyConcurrency(apiKey, concurrencyLimit)
			if !ok {
				current := keyConcurrencyCount(apiKey)
				rejectQuotaLimit(c, "concurrency", float64(concurrencyLimit), float64(current), "concurrency_limit_exceeded",
					fmt.Sprintf("Concurrent request limit exceeded: %d in-flight requests (limit %d). Wait for running requests to finish, or raise the concurrency limit in the permission profile.", current, concurrencyLimit))
				return
			}
			defer release()
		}

		// --- RPM check (sliding window, in-memory) ---
		if rpmLimit > 0 {
			currentRPM := rpmTracker.count()
			if currentRPM > rpmLimit {
				rejectQuotaLimit(c, "rpm", float64(rpmLimit), float64(currentRPM), "rpm_limit_exceeded",
					fmt.Sprintf("Requests-per-minute (RPM) limit exceeded: %d/%d requests in the last minute. Slow down, or raise the RPM limit in the permission profile.", currentRPM, rpmLimit))
				return
			}
		}

		// --- TPM check (sliding window, in-memory) ---
		if tpmLimit > 0 {
			tracker := getTPMTracker(apiKey)
			currentTPM := tracker.sum()
			if currentTPM >= int64(tpmLimit) {
				rejectQuotaLimit(c, "tpm", float64(tpmLimit), float64(currentTPM), "tpm_limit_exceeded",
					fmt.Sprintf("Tokens-per-minute (TPM) limit exceeded: %d/%d tokens in the last minute. Slow down, or raise the TPM limit in the permission profile.", currentTPM, tpmLimit))
				return
			}
		}

		// --- Daily limit check (from usage DB) ---
		if dailyLimit > 0 {
			todayCount, err := countTodayByKeyFunc(apiKey)
			if err != nil {
				log.Warnf("quota: failed to query daily usage for key %s: %v", maskKey(apiKey), err)
			} else if todayCount >= int64(dailyLimit) {
				rejectQuotaLimit(c, "daily", float64(dailyLimit), float64(todayCount), "daily_limit_exceeded",
					fmt.Sprintf("Daily request limit exceeded: %d/%d requests used today. Raise the daily request limit in the permission profile, or wait until the next project day.", todayCount, dailyLimit))
				return
			}
		}

		// --- Total quota check (from usage DB) ---
		if totalQuota > 0 {
			totalCount, err := countTotalByKeyFunc(apiKey)
			if err != nil {
				log.Warnf("quota: failed to query total usage for key %s: %v", maskKey(apiKey), err)
			} else if totalCount >= int64(totalQuota) {
				rejectQuotaLimit(c, "total", float64(totalQuota), float64(totalCount), "total_quota_exceeded",
					fmt.Sprintf("Total request quota exhausted: %d/%d lifetime requests used. Raise the total request quota in the permission profile to continue.", totalCount, totalQuota))
				return
			}
		}

		// --- Spending limit check (from usage DB) ---
		if spendingLimit > 0 {
			totalCost, err := queryTotalCostByKeyFunc(apiKey)
			if err != nil {
				log.Warnf("quota: failed to query total cost for key %s: %v", maskKey(apiKey), err)
			} else if totalCost >= spendingLimit {
				rejectQuotaLimit(c, "spending", spendingLimit, totalCost, "spending_limit_exceeded",
					fmt.Sprintf("Lifetime spending limit exceeded: $%.2f of $%.2f used. Raise the spending limit to continue.", totalCost, spendingLimit))
				return
			}
		}

		// --- Daily spending limit check (from usage DB) ---
		if dailySpendingLimit > 0 {
			todayCost, err := queryTodayCostByKeyFunc(apiKey)
			if err != nil {
				log.Warnf("quota: failed to query today cost for key %s: %v", maskKey(apiKey), err)
			} else if todayCost >= dailySpendingLimit {
				rejectQuotaLimit(c, "daily_spending", dailySpendingLimit, todayCost, "daily_spending_limit_exceeded",
					fmt.Sprintf("Daily spending limit exceeded: $%.2f of $%.2f used today. Raise the daily spending limit in the permission profile, reset today's spending, or wait until the next project day.", todayCost, dailySpendingLimit))
				return
			}
		}

		c.Next()
	}
}

// rejectQuotaLimit writes a 429 with a distinct code/message and diagnostic headers.
// Headers help clients that only surface "429 Too Many Requests" after retries.
func rejectQuotaLimit(c *gin.Context, rejectedBy string, limit, current float64, code, message string) {
	const errType = "rate_limit_exceeded"
	diagnostics.SetQuotaRejection(c, rejectedBy, limit, current, code, errType, message)
	c.Header("X-CliRelay-Quota-Code", code)
	c.Header("X-CliRelay-Quota-Limit", formatQuotaNumber(limit))
	c.Header("X-CliRelay-Quota-Current", formatQuotaNumber(current))
	c.Header("X-CliRelay-Quota-Rejected-By", rejectedBy)
	c.AbortWithStatusJSON(http.StatusTooManyRequests, gin.H{
		"error": map[string]interface{}{
			"message": message,
			"type":    errType,
			"code":    code,
		},
	})
}

func formatQuotaNumber(v float64) string {
	if v == float64(int64(v)) {
		return strconv.FormatInt(int64(v), 10)
	}
	return strconv.FormatFloat(v, 'f', -1, 64)
}

// ─── Usage DB query functions (injected to avoid import cycle) ──────────────

// countTodayByKeyFunc and countTotalByKeyFunc are set by InitQuotaUsageFuncs.
// They default to no-ops that always return 0 (no limit enforced) until set.
var (
	countTodayByKeyFunc     = func(string) (int64, error) { return 0, nil }
	countTotalByKeyFunc     = func(string) (int64, error) { return 0, nil }
	queryTotalCostByKeyFunc = func(string) (float64, error) { return 0, nil }
	queryTodayCostByKeyFunc = func(string) (float64, error) { return 0, nil }
)

// InitQuotaUsageFuncs injects the usage DB query functions into the middleware.
// This avoids a direct import of the usage package which would cause cycles.
func InitQuotaUsageFuncs(
	countToday func(string) (int64, error),
	countTotal func(string) (int64, error),
	totalCost func(string) (float64, error),
	todayCost func(string) (float64, error),
) {
	countTodayByKeyFunc = countToday
	countTotalByKeyFunc = countTotal
	queryTotalCostByKeyFunc = totalCost
	queryTodayCostByKeyFunc = todayCost
}

// ─── Helpers ────────────────────────────────────────────────────────────────

func parseIntMetadata(metadata map[string]string, key string) int {
	v, ok := metadata[key]
	if !ok {
		return 0
	}
	n, err := strconv.Atoi(strings.TrimSpace(v))
	if err != nil {
		return 0
	}
	return n
}

func acquireKeyConcurrency(apiKey string, limit int) (func(), bool) {
	if apiKey == "" || limit <= 0 {
		return func() {}, true
	}

	inFlightMu.Lock()
	defer inFlightMu.Unlock()

	if inFlightByKey[apiKey] >= limit {
		return nil, false
	}
	inFlightByKey[apiKey]++

	return func() {
		inFlightMu.Lock()
		defer inFlightMu.Unlock()

		current := inFlightByKey[apiKey]
		if current <= 1 {
			delete(inFlightByKey, apiKey)
			return
		}
		inFlightByKey[apiKey] = current - 1
	}, true
}

func keyConcurrencyCount(apiKey string) int {
	if apiKey == "" {
		return 0
	}
	inFlightMu.Lock()
	defer inFlightMu.Unlock()
	return inFlightByKey[apiKey]
}

func maskKey(key string) string {
	if len(key) <= 8 {
		return "***"
	}
	return key[:4] + "..." + key[len(key)-4:]
}

func parseFloatMetadata(metadata map[string]string, key string) float64 {
	v, ok := metadata[key]
	if !ok {
		return 0
	}
	n, err := strconv.ParseFloat(strings.TrimSpace(v), 64)
	if err != nil {
		return 0
	}
	return n
}

// ─── Dashboard snapshot (for system_stats) ──────────────────────────────────

// ConcurrencySnapshot represents real-time rate info for a single API key.
type ConcurrencySnapshot struct {
	APIKey   string `json:"api_key"`
	RPM      int    `json:"rpm"`       // current requests in the last 60s
	TPM      int64  `json:"tpm"`       // current tokens in the last 60s
	RPMLimit int    `json:"rpm_limit"` // configured limit (0 = unlimited)
	TPMLimit int    `json:"tpm_limit"` // configured limit (0 = unlimited)
}

// snapshotLimits stores the configured limits per key for dashboard display.
var snapshotLimits sync.Map // map[string][2]int  {rpmLimit, tpmLimit}

// UpdateKeyLimits stores the configured RPM/TPM limits for a key so the
// dashboard snapshot can display them. Called during auth.
func UpdateKeyLimits(apiKey string, rpmLimit, tpmLimit int) {
	if apiKey == "" {
		return
	}
	snapshotLimits.Store(apiKey, [2]int{rpmLimit, tpmLimit})
}

// GetConcurrencySnapshot returns a list of API keys with active RPM/TPM usage
// and the total in-flight request count (sum of all RPM counters).
func GetConcurrencySnapshot() ([]ConcurrencySnapshot, int64) {
	var snapshots []ConcurrencySnapshot
	var totalInFlight int64

	rpmTrackers.Range(func(key, value any) bool {
		apiKey := key.(string)
		w := value.(*slidingWindow)
		rpm := w.count()

		var tpm int64
		if tv, ok := tpmTrackers.Load(apiKey); ok {
			tpm = tv.(*tokenWindow).sum()
		}

		if rpm > 0 || tpm > 0 {
			snap := ConcurrencySnapshot{
				APIKey: apiKey,
				RPM:    rpm,
				TPM:    tpm,
			}
			if limits, ok := snapshotLimits.Load(apiKey); ok {
				l := limits.([2]int)
				snap.RPMLimit = l[0]
				snap.TPMLimit = l[1]
			}
			snapshots = append(snapshots, snap)
			totalInFlight += int64(rpm)
		}
		return true
	})

	return snapshots, totalInFlight
}
