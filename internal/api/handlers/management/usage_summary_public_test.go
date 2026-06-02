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
		keyWithData    = "sk-test-key-with-usage"
		keyWithoutData = "sk-test-key-empty"
	)

	insertTestLog(t, keyWithData)
	insertTestLog(t, keyWithData)

	t.Run("returns today stats for key with usage", func(t *testing.T) {
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

	t.Run("returns found=false for key without usage", func(t *testing.T) {
		body := []byte(`{"api_key":"` + keyWithoutData + `"}`)
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
			t.Errorf("found = true, want false")
		}
		if got.Stats.TotalCalls != 0 {
			t.Errorf("total_calls = %d, want 0", got.Stats.TotalCalls)
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
}
