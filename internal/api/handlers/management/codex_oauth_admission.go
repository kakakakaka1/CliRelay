package management

import (
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/router-for-me/CLIProxyAPI/v6/internal/codexadmission"
	"github.com/router-for-me/CLIProxyAPI/v6/internal/config"
	settingsstore "github.com/router-for-me/CLIProxyAPI/v6/internal/management/settings/store"
)

type codexOAuthAdmissionResponse struct {
	AllowedClients          []string                                 `json:"allowed_clients"`
	AvailableAllowedClients []codexadmission.AllowedClientPresetInfo `json:"available_allowed_clients"`
	CodexOAuthAdmission     config.CodexOAuthAdmissionConfig         `json:"codex-oauth-admission"`
}

type codexOAuthAdmissionRequest struct {
	AllowedClients []string `json:"allowed_clients"`
}

func (h *Handler) GetCodexOAuthAdmission(c *gin.Context) {
	cfg := h.providerConfigForTenant(c)
	current := config.CodexOAuthAdmissionConfig{}
	if cfg != nil {
		current = cfg.CodexOAuthAdmission
	}

	current = config.CleanCodexOAuthAdmission(current)
	c.JSON(http.StatusOK, codexOAuthAdmissionResponse{
		AllowedClients:          append([]string(nil), current.AllowedClientPresets...),
		AvailableAllowedClients: codexadmission.AvailableAllowedClientPresets(),
		CodexOAuthAdmission:     current,
	})
}

func (h *Handler) PutCodexOAuthAdmission(c *gin.Context) {
	var body codexOAuthAdmissionRequest
	if err := c.ShouldBindJSON(&body); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid body"})
		return
	}
	allowedClients, err := codexadmission.NormalizeAllowedClientPresets(body.AllowedClients)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	next := config.CodexOAuthAdmissionConfig{AllowedClientPresets: allowedClients}

	cfg := h.providerConfigForTenant(c)
	if cfg == nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": "config unavailable"})
		return
	}
	previous := cfg.CodexOAuthAdmission
	cfg.CodexOAuthAdmission = next
	if !h.persistRuntimeSettingForTenant(c, settingsstore.RuntimeSettingCodexOAuthAdmission, next, cfg) {
		cfg.CodexOAuthAdmission = previous
	}
}
