package management

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/router-for-me/CLIProxyAPI/v6/internal/config"
	"github.com/router-for-me/CLIProxyAPI/v6/internal/usage"
)

func TestGetCodexFingerprintRecommendationsReturnsLogDerivedCandidates(t *testing.T) {
	gin.SetMode(gin.TestMode)

	dbPath := filepath.Join(t.TempDir(), "usage.db")
	if err := usage.InitDB(dbPath, config.RequestLogStorageConfig{
		StoreContent:           true,
		ContentRetentionDays:   30,
		CleanupIntervalMinutes: 1440,
	}, time.UTC); err != nil {
		t.Fatalf("InitDB: %v", err)
	}
	t.Cleanup(usage.CloseDB)

	detail, err := json.Marshal(map[string]any{
		"client": map[string]any{
			"ip":     "203.0.113.20",
			"method": "POST",
			"path":   "/v1/responses",
			"host":   "relay.test",
			"fingerprint_headers": map[string][]string{
				"User-Agent":            {"codex-tui/0.125.0 (Mac OS 26.5; arm64)"},
				"Version":               {"0.125.0"},
				"Originator":            {"codex-tui"},
				"X-Codex-Beta-Features": {"exec_command_v2"},
				"Session_id":            {"session-handler-secret"},
			},
		},
	})
	if err != nil {
		t.Fatalf("marshal detail: %v", err)
	}
	usage.InsertLogWithDetails("sk-test", "Primary", "gpt-5.4", "codex", "Codex", "auth-1", false, time.Now().UTC(), 100, 10, usage.TokenStats{
		InputTokens: 1, OutputTokens: 1, TotalTokens: 2,
	}, "", "", string(detail))

	h := &Handler{cfg: &config.Config{}}
	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Request = httptest.NewRequest(http.MethodGet, "/identity-fingerprint/codex/recommendations?days=7&limit=20", nil)

	h.GetCodexFingerprintRecommendations(c)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d, body=%s", rec.Code, http.StatusOK, rec.Body.String())
	}

	var payload struct {
		Items []struct {
			Count       int `json:"count"`
			Recommended struct {
				Enabled       bool              `json:"enabled"`
				UserAgent     string            `json:"user-agent"`
				Version       string            `json:"version"`
				Originator    string            `json:"originator"`
				BetaFeatures  string            `json:"x-codex-beta-features"`
				SessionID     string            `json:"session-id"`
				CustomHeaders map[string]string `json:"custom-headers"`
			} `json:"recommended"`
			IgnoredHeaders map[string]string `json:"ignored_headers"`
		} `json:"items"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &payload); err != nil {
		t.Fatalf("unmarshal response: %v", err)
	}
	if len(payload.Items) != 1 {
		t.Fatalf("items length = %d, want 1", len(payload.Items))
	}
	item := payload.Items[0]
	if item.Count != 1 {
		t.Fatalf("Count = %d, want 1", item.Count)
	}
	if !item.Recommended.Enabled {
		t.Fatalf("recommended fingerprint should enable Codex identity")
	}
	if item.Recommended.UserAgent != "codex-tui/0.125.0 (Mac OS 26.5; arm64)" {
		t.Fatalf("user-agent = %q", item.Recommended.UserAgent)
	}
	if item.Recommended.Version != "0.125.0" {
		t.Fatalf("version = %q", item.Recommended.Version)
	}
	if item.Recommended.SessionID != "" {
		t.Fatalf("session-id = %q, want empty", item.Recommended.SessionID)
	}
	if item.Recommended.BetaFeatures != "exec_command_v2" {
		t.Fatalf("x-codex-beta-features = %q", item.Recommended.BetaFeatures)
	}
	if _, exists := item.Recommended.CustomHeaders["X-Codex-Beta-Features"]; exists {
		t.Fatalf("X-Codex-Beta-Features must be returned as a managed field, not a custom header")
	}
	if item.IgnoredHeaders["Session_id"] == "session-handler-secret" {
		t.Fatalf("session id must be masked in ignored headers")
	}
}
