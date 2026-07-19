package api

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/router-for-me/CLIProxyAPI/v6/internal/config"
	"github.com/router-for-me/CLIProxyAPI/v6/internal/identity"
	"github.com/router-for-me/CLIProxyAPI/v6/internal/registry"
	internalrouting "github.com/router-for-me/CLIProxyAPI/v6/internal/routing"
	"github.com/router-for-me/CLIProxyAPI/v6/sdk/api/handlers"
	"github.com/router-for-me/CLIProxyAPI/v6/sdk/api/handlers/claude"
	"github.com/router-for-me/CLIProxyAPI/v6/sdk/api/handlers/openai"
	coreauth "github.com/router-for-me/CLIProxyAPI/v6/sdk/cliproxy/auth"
)

func TestUnifiedModelsHandlerScopesBusinessTenantAndAllowedModels(t *testing.T) {
	gin.SetMode(gin.TestMode)
	const businessTenant = "cccccccc-dddd-eeee-ffff-000000000001"

	modelRegistry := registry.GetGlobalRegistry()
	businessAuthID := "models-business-tenant-auth"
	systemAuthID := "models-system-tenant-auth"
	modelRegistry.RegisterClient(businessAuthID, "openai", []*registry.ModelInfo{
		{ID: "gpt-business-only"},
		{ID: "codex-business-only"},
		{ID: "grok-business-only"},
	})
	modelRegistry.RegisterClient(systemAuthID, "openai", []*registry.ModelInfo{
		{ID: "gpt-system-only"},
		{ID: "codex-system-only"},
		{ID: "grok-system-only"},
	})
	t.Cleanup(func() {
		modelRegistry.UnregisterClient(businessAuthID)
		modelRegistry.UnregisterClient(systemAuthID)
	})

	authManager := coreauth.NewManager(nil, nil, nil)
	authManager.SetConfigForTenant(businessTenant, &config.Config{})
	authManager.SetConfigForTenant(identity.SystemTenantID, &config.Config{})
	if _, err := authManager.Register(context.Background(), &coreauth.Auth{
		ID: businessAuthID, TenantID: businessTenant, Provider: "openai", Status: coreauth.StatusActive,
	}); err != nil {
		t.Fatalf("register business auth: %v", err)
	}
	if _, err := authManager.Register(context.Background(), &coreauth.Auth{
		ID: systemAuthID, TenantID: identity.SystemTenantID, Provider: "openai", Status: coreauth.StatusActive,
	}); err != nil {
		t.Fatalf("register system auth: %v", err)
	}

	cfg := &config.Config{}
	base := handlers.NewBaseAPIHandlers(&cfg.SDKConfig, authManager)
	server := &Server{handlers: base, cfg: cfg}
	openaiHandler := openai.NewOpenAIAPIHandler(base)
	claudeHandler := claude.NewClaudeCodeAPIHandler(base)
	router := gin.New()
	router.GET("/v1/models", func(c *gin.Context) {
		c.Set("tenantID", businessTenant)
		if c.Query("restricted") == "1" {
			c.Set("accessMetadata", map[string]string{"allowed-models": "gpt-business-only,grok-business-only"})
		}
		server.unifiedModelsHandler(openaiHandler, claudeHandler)(c)
	})

	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/v1/models", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body=%s", rec.Code, rec.Body.String())
	}
	var response struct {
		Data []map[string]interface{} `json:"data"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &response); err != nil {
		t.Fatalf("unmarshal response: %v", err)
	}
	ids := modelIDs(response.Data)
	for _, want := range []string{"gpt-business-only", "codex-business-only", "grok-business-only"} {
		found := false
		for _, id := range ids {
			found = found || id == want
		}
		if !found {
			t.Fatalf("business tenant models = %#v, missing %q", ids, want)
		}
	}
	for _, forbidden := range []string{"gpt-system-only", "codex-system-only", "grok-system-only"} {
		for _, id := range ids {
			if id == forbidden {
				t.Fatalf("business tenant response leaked system model %q: %#v", forbidden, ids)
			}
		}
	}

	restrictedRec := httptest.NewRecorder()
	router.ServeHTTP(restrictedRec, httptest.NewRequest(http.MethodGet, "/v1/models?restricted=1", nil))
	if restrictedRec.Code != http.StatusOK {
		t.Fatalf("restricted status = %d, body=%s", restrictedRec.Code, restrictedRec.Body.String())
	}
	response.Data = nil
	if err := json.Unmarshal(restrictedRec.Body.Bytes(), &response); err != nil {
		t.Fatalf("unmarshal restricted response: %v", err)
	}
	ids = modelIDs(response.Data)
	if !sameStrings(ids, []string{"gpt-business-only", "grok-business-only"}) &&
		!sameStrings(ids, []string{"grok-business-only", "gpt-business-only"}) {
		t.Fatalf("business tenant models = %#v, want allowed business GPT+Grok only", ids)
	}
	for _, forbidden := range []string{"gpt-system-only", "codex-system-only", "grok-system-only", "codex-business-only"} {
		for _, id := range ids {
			if id == forbidden {
				t.Fatalf("business tenant response leaked forbidden model %q: %#v", forbidden, ids)
			}
		}
	}
}

func TestFilterCodexModelsForCcSwitchRouteReturnsRequestModels(t *testing.T) {
	models := []map[string]interface{}{
		{"id": "deepseek-chat", "object": "model", "owned_by": "deepseek"},
		{"id": "kimi-k2", "object": "model", "owned_by": "moonshot"},
		{"id": "gpt-5.5", "object": "model", "owned_by": "openai"},
	}
	route := &internalrouting.PathRouteContext{
		CcSwitch: &internalrouting.CcSwitchRouteContext{
			ClientType:   "codex",
			DefaultModel: "deepseek-v4-flash",
			ModelMappings: []internalrouting.CcSwitchModelMapping{
				{RequestModel: "deepseek-v4-flash", TargetModel: "deepseek-chat"},
				{RequestModel: "deepseek-v4-pro", TargetModel: "deepseek-chat"},
			},
		},
	}

	filtered := filterModelsForCcSwitchRoute(models, route)
	got := modelIDs(filtered)
	want := []string{"deepseek-v4-flash", "deepseek-v4-pro"}
	if !sameStrings(got, want) {
		t.Fatalf("model ids = %#v, want %#v", got, want)
	}
	if filtered[0]["owned_by"] != "deepseek" {
		t.Fatalf("owned_by = %v, want deepseek", filtered[0]["owned_by"])
	}
}

func TestCcSwitchRequestModelAllowedForTarget(t *testing.T) {
	route := &internalrouting.PathRouteContext{
		CcSwitch: &internalrouting.CcSwitchRouteContext{
			ModelMappings: []internalrouting.CcSwitchModelMapping{
				{RequestModel: "deepseek-v4-flash", TargetModel: "deepseek-chat"},
			},
		},
	}

	if !ccSwitchRequestModelAllowedForTarget("deepseek-chat", route, map[string]struct{}{"deepseek-v4-flash": {}}) {
		t.Fatal("request model alias was not allowed for target")
	}
	if ccSwitchRequestModelAllowedForTarget("kimi-k2", route, map[string]struct{}{"deepseek-v4-flash": {}}) {
		t.Fatal("unmapped target was allowed")
	}
}

func TestEnrichOpenAIModelsWithCatalogLeavesUnknownModels(t *testing.T) {
	models := []map[string]interface{}{
		{"id": "unknown-model-xyz", "object": "model"},
	}
	enrichOpenAIModelsWithCatalog("", models)
	if _, hasPricing := models[0]["pricing"]; hasPricing {
		t.Fatal("unexpected pricing for unknown model")
	}
	if _, hasDesc := models[0]["description"]; hasDesc {
		t.Fatal("unexpected description for unknown model")
	}
}

func modelIDs(models []map[string]interface{}) []string {
	ids := make([]string, 0, len(models))
	for _, model := range models {
		if id, ok := model["id"].(string); ok {
			ids = append(ids, id)
		}
	}
	return ids
}

func sameStrings(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
