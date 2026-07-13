package management

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/router-for-me/CLIProxyAPI/v6/internal/config"
	"github.com/router-for-me/CLIProxyAPI/v6/internal/usage"
)

func setupUsageSummaryTestDB(t *testing.T) {
	t.Helper()
	tmpFile, err := os.CreateTemp("", "usage-summary-*.db")
	if err != nil {
		t.Fatalf("create temp db: %v", err)
	}
	dbPath := tmpFile.Name()
	_ = tmpFile.Close()
	t.Cleanup(func() {
		usage.CloseDB()
		_ = os.Remove(dbPath)
		_ = os.Remove(dbPath + "-wal")
		_ = os.Remove(dbPath + "-shm")
	})

	if err := usage.InitDB(dbPath, config.RequestLogStorageConfig{}, time.UTC); err != nil {
		t.Fatalf("InitDB: %v", err)
	}
}

func insertTestLog(t *testing.T, apiKey string) {
	t.Helper()
	usage.InsertLog(apiKey, "test", "gpt-4", "test", "chan", "idx", false, time.Now(), 100, 50,
		usage.TokenStats{InputTokens: 10, OutputTokens: 20, TotalTokens: 30},
		"", "",
	)
}

func TestGetPublicUsageSummary(t *testing.T) {
	gin.SetMode(gin.TestMode)
	setupUsageSummaryTestDB(t)

	const (
		keyWithData = "sk-test-key-with-usage"
		keyNoUsage  = "sk-test-key-no-usage"
		keyDisabled = "sk-test-key-disabled"
		keyUnknown  = "sk-test-key-unknown"
	)

	insertTestLog(t, keyWithData)
	insertTestLog(t, keyWithData)

	if err := usage.ReplaceAllAPIKeys([]usage.APIKeyRow{
		{Key: keyWithData, Disabled: false},
		{Key: keyNoUsage, Disabled: false},
		{Key: keyDisabled, Disabled: true},
	}); err != nil {
		t.Fatalf("ReplaceAllAPIKeys: %v", err)
	}

	t.Run("found=true for key with usage today", func(t *testing.T) {
		body := []byte(`{"api_key":"` + keyWithData + `"}`)
		rec := httptest.NewRecorder()
		ctx, _ := gin.CreateTestContext(rec)
		ctx.Request = httptest.NewRequest(http.MethodPost, "/public/usage/summary", bytes.NewReader(body))
		ctx.Request.Header.Set("Content-Type", "application/json")

		h := NewHandler(&config.Config{}, "", nil)
		h.GetPublicUsageSummary(ctx)

		if rec.Code != http.StatusOK {
			t.Fatalf("status = %d, want %d; body=%s", rec.Code, http.StatusOK, rec.Body.String())
		}

		var got struct {
			Found bool   `json:"found"`
			Range string `json:"range"`
			Stats struct {
				TotalCalls int64   `json:"total_calls"`
				QuotaCost  float64 `json:"quota_cost"`
			} `json:"stats"`
		}
		if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
			t.Fatalf("unmarshal: %v", err)
		}
		if !got.Found {
			t.Errorf("found = false, want true")
		}
		if got.Range != "today" {
			t.Errorf("range = %q, want %q", got.Range, "today")
		}
		if got.Stats.TotalCalls != 2 {
			t.Errorf("total_calls = %d, want 2", got.Stats.TotalCalls)
		}
		if got.Stats.QuotaCost < 0 {
			t.Errorf("quota_cost = %f, want >= 0", got.Stats.QuotaCost)
		}
	})

	t.Run("found=true for existing key with no usage today", func(t *testing.T) {
		body := []byte(`{"api_key":"` + keyNoUsage + `"}`)
		rec := httptest.NewRecorder()
		ctx, _ := gin.CreateTestContext(rec)
		ctx.Request = httptest.NewRequest(http.MethodPost, "/public/usage/summary", bytes.NewReader(body))
		ctx.Request.Header.Set("Content-Type", "application/json")

		h := NewHandler(&config.Config{}, "", nil)
		h.GetPublicUsageSummary(ctx)

		if rec.Code != http.StatusOK {
			t.Fatalf("status = %d, want %d; body=%s", rec.Code, http.StatusOK, rec.Body.String())
		}

		var got struct {
			Found bool   `json:"found"`
			Range string `json:"range"`
			Stats struct {
				TotalCalls int64   `json:"total_calls"`
				QuotaCost  float64 `json:"quota_cost"`
			} `json:"stats"`
		}
		if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
			t.Fatalf("unmarshal: %v", err)
		}
		if !got.Found {
			t.Errorf("found = false, want true (key exists in api_keys table)")
		}
		if got.Stats.TotalCalls != 0 {
			t.Errorf("total_calls = %d, want 0", got.Stats.TotalCalls)
		}
	})

	t.Run("found=false for disabled key even with usage logs", func(t *testing.T) {
		body := []byte(`{"api_key":"` + keyDisabled + `"}`)
		rec := httptest.NewRecorder()
		ctx, _ := gin.CreateTestContext(rec)
		ctx.Request = httptest.NewRequest(http.MethodPost, "/public/usage/summary", bytes.NewReader(body))
		ctx.Request.Header.Set("Content-Type", "application/json")

		h := NewHandler(&config.Config{}, "", nil)
		h.GetPublicUsageSummary(ctx)

		if rec.Code != http.StatusOK {
			t.Fatalf("status = %d, want %d; body=%s", rec.Code, http.StatusOK, rec.Body.String())
		}

		var got struct {
			Found bool   `json:"found"`
			Range string `json:"range"`
			Stats struct {
				TotalCalls int64   `json:"total_calls"`
				QuotaCost  float64 `json:"quota_cost"`
			} `json:"stats"`
		}
		if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
			t.Fatalf("unmarshal: %v", err)
		}
		if got.Found {
			t.Errorf("found = true, want false (key is disabled)")
		}
	})

	t.Run("found=false for unknown key", func(t *testing.T) {
		body := []byte(`{"api_key":"` + keyUnknown + `"}`)
		rec := httptest.NewRecorder()
		ctx, _ := gin.CreateTestContext(rec)
		ctx.Request = httptest.NewRequest(http.MethodPost, "/public/usage/summary", bytes.NewReader(body))
		ctx.Request.Header.Set("Content-Type", "application/json")

		h := NewHandler(&config.Config{}, "", nil)
		h.GetPublicUsageSummary(ctx)

		if rec.Code != http.StatusOK {
			t.Fatalf("status = %d, want %d; body=%s", rec.Code, http.StatusOK, rec.Body.String())
		}

		var got struct {
			Found bool   `json:"found"`
			Range string `json:"range"`
			Stats struct {
				TotalCalls int64   `json:"total_calls"`
				QuotaCost  float64 `json:"quota_cost"`
			} `json:"stats"`
		}
		if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
			t.Fatalf("unmarshal: %v", err)
		}
		if got.Found {
			t.Errorf("found = true, want false (unknown key)")
		}
	})

	t.Run("returns 400 for empty api_key", func(t *testing.T) {
		body := []byte(`{}`)
		rec := httptest.NewRecorder()
		ctx, _ := gin.CreateTestContext(rec)
		ctx.Request = httptest.NewRequest(http.MethodPost, "/public/usage/summary", bytes.NewReader(body))

		h := NewHandler(&config.Config{}, "", nil)
		h.GetPublicUsageSummary(ctx)

		if rec.Code != http.StatusBadRequest {
			t.Fatalf("status = %d, want 400; body=%s", rec.Code, rec.Body.String())
		}
	})

	t.Run("returns 400 for missing body", func(t *testing.T) {
		rec := httptest.NewRecorder()
		ctx, _ := gin.CreateTestContext(rec)
		ctx.Request = httptest.NewRequest(http.MethodPost, "/public/usage/summary", nil)

		h := NewHandler(&config.Config{}, "", nil)
		h.GetPublicUsageSummary(ctx)

		if rec.Code != http.StatusBadRequest {
			t.Fatalf("status = %d, want 400; body=%s", rec.Code, rec.Body.String())
		}
	})

	t.Run("returns 400 when api_key is passed as query param with empty body", func(t *testing.T) {
		rec := httptest.NewRecorder()
		ctx, _ := gin.CreateTestContext(rec)
		ctx.Request = httptest.NewRequest(http.MethodPost, "/public/usage/summary?api_key="+keyWithData, nil)

		h := NewHandler(&config.Config{}, "", nil)
		h.GetPublicUsageSummary(ctx)

		if rec.Code != http.StatusBadRequest {
			t.Fatalf("status = %d, want 400 (query param api_key must be rejected); body=%s", rec.Code, rec.Body.String())
		}
	})
}

// Regression: multi-tenant keys must not be looked up under the system tenant.
// CC Switch polls this endpoint with the raw API key only; without ResolveAPIKeyTenant
// the card always shows 0 calls / $0 even when request_logs has real usage.
func TestGetPublicUsageSummary_ResolvesBusinessTenant(t *testing.T) {
	gin.SetMode(gin.TestMode)
	setupUsageSummaryTestDB(t)

	const (
		tenantID = "00000000-0000-0000-0000-0000000000aa"
		apiKey   = "sk-business-tenant-usage"
	)

	if err := usage.UpsertAPIKeyForTenant(tenantID, usage.APIKeyRow{
		Key:      apiKey,
		Name:     "business-user",
		Disabled: false,
	}); err != nil {
		t.Fatalf("UpsertAPIKeyForTenant: %v", err)
	}

	// InsertLog derives tenant from the API key row, so this lands in the business tenant.
	usage.InsertLog(apiKey, "business-user", "gpt-5.4", "test", "chan", "idx", false, time.Now(), 100, 50,
		usage.TokenStats{InputTokens: 10, OutputTokens: 20, TotalTokens: 30},
		"", "",
	)
	usage.InsertLog(apiKey, "business-user", "gpt-5.4", "test", "chan", "idx", false, time.Now(), 100, 50,
		usage.TokenStats{InputTokens: 5, OutputTokens: 5, TotalTokens: 10},
		"", "",
	)

	// Sanity: system-tenant lookup must not see this key (precondition of the bug).
	if row := usage.GetAPIKey(apiKey); row != nil {
		t.Fatalf("precondition failed: GetAPIKey (system tenant) unexpectedly found business key")
	}
	if row := usage.GetAPIKeyForTenant(tenantID, apiKey); row == nil {
		t.Fatalf("precondition failed: business tenant key missing")
	}

	body := []byte(`{"api_key":"` + apiKey + `"}`)
	rec := httptest.NewRecorder()
	ctx, _ := gin.CreateTestContext(rec)
	ctx.Request = httptest.NewRequest(http.MethodPost, "/public/usage/summary", bytes.NewReader(body))
	ctx.Request.Header.Set("Content-Type", "application/json")

	h := NewHandler(&config.Config{}, "", nil)
	h.GetPublicUsageSummary(ctx)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d; body=%s", rec.Code, http.StatusOK, rec.Body.String())
	}

	var got struct {
		Found bool   `json:"found"`
		Range string `json:"range"`
		Stats struct {
			TotalCalls int64   `json:"total_calls"`
			QuotaCost  float64 `json:"quota_cost"`
		} `json:"stats"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if !got.Found {
		t.Errorf("found = false, want true for business-tenant key")
	}
	if got.Stats.TotalCalls != 2 {
		t.Errorf("total_calls = %d, want 2", got.Stats.TotalCalls)
	}
}
