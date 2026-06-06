package management

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/router-for-me/CLIProxyAPI/v6/internal/api/bodyutil"
	managementauthfiles "github.com/router-for-me/CLIProxyAPI/v6/internal/management/authfiles"
	antigravityprovider "github.com/router-for-me/CLIProxyAPI/v6/internal/management/oauth/providers/antigravity"
	claudeprovider "github.com/router-for-me/CLIProxyAPI/v6/internal/management/oauth/providers/claude"
	codexprovider "github.com/router-for-me/CLIProxyAPI/v6/internal/management/oauth/providers/codex"
	geminicli "github.com/router-for-me/CLIProxyAPI/v6/internal/management/oauth/providers/geminicli"
	iflowprovider "github.com/router-for-me/CLIProxyAPI/v6/internal/management/oauth/providers/iflow"
	kimiprovider "github.com/router-for-me/CLIProxyAPI/v6/internal/management/oauth/providers/kimi"
	qwenprovider "github.com/router-for-me/CLIProxyAPI/v6/internal/management/oauth/providers/qwen"
	"github.com/router-for-me/CLIProxyAPI/v6/internal/registry"
	sdkAuth "github.com/router-for-me/CLIProxyAPI/v6/sdk/auth"
	coreauth "github.com/router-for-me/CLIProxyAPI/v6/sdk/cliproxy/auth"
	log "github.com/sirupsen/logrus"
)

const (
	anthropicCallbackPort    = 54545
	geminiCallbackPort       = 8085
	codexCallbackPort        = 1455
	oauthCallbackWaitTimeout = oauthSessionTTL
)

func isWebUIRequest(c *gin.Context) bool {
	raw := strings.TrimSpace(c.Query("is_webui"))
	if raw == "" {
		return false
	}
	switch strings.ToLower(raw) {
	case "1", "true", "yes", "on":
		return true
	default:
		return false
	}
}

func (h *Handler) managementCallbackURL(path string) (string, error) {
	if h == nil || h.cfg == nil || h.cfg.Port <= 0 {
		return "", fmt.Errorf("server port is not configured")
	}
	if !strings.HasPrefix(path, "/") {
		path = "/" + path
	}
	scheme := "http"
	if h.cfg.TLS.Enable {
		scheme = "https"
	}
	return fmt.Sprintf("%s://127.0.0.1:%d%s", scheme, h.cfg.Port, path), nil
}

func (h *Handler) ListAuthFiles(c *gin.Context) {
	if h == nil {
		c.JSON(500, gin.H{"error": "handler not initialized"})
		return
	}
	if h.authManager == nil {
		h.listAuthFilesFromDisk(c)
		return
	}
	files := managementauthfiles.ListEntries(h.authManager.List(), managementauthfiles.EntryOptions{
		OnStatError: func(path string, err error) {
			log.WithError(err).Warnf("failed to stat auth file %s", path)
		},
	})
	c.JSON(200, gin.H{"files": files})
}

// GetAuthFileModels returns the models supported by a specific auth file
func (h *Handler) GetAuthFileModels(c *gin.Context) {
	name := c.Query("name")
	if name == "" {
		c.JSON(400, gin.H{"error": "name is required"})
		return
	}

	models := managementauthfiles.ListModelEntries(h.authManager, registry.GetGlobalRegistry(), name)
	c.JSON(200, gin.H{"models": models})
}

// List auth files from disk when the auth manager is unavailable.
func (h *Handler) listAuthFilesFromDisk(c *gin.Context) {
	files, err := managementauthfiles.ListDiskEntries(h.cfg.AuthDir, time.Now())
	if err != nil {
		c.JSON(500, gin.H{"error": fmt.Sprintf("failed to read auth dir: %v", err)})
		return
	}
	c.JSON(200, gin.H{"files": files})
}

// Download single auth file by name
func (h *Handler) DownloadAuthFile(c *gin.Context) {
	name, errValidate := managementauthfiles.ValidateFileQueryName(c.Query("name"), true)
	if errValidate != nil {
		c.JSON(400, gin.H{"error": errValidate.Error()})
		return
	}
	full := managementauthfiles.FilePath(h.cfg.AuthDir, name)
	_, err := os.Stat(full)
	if err != nil {
		if os.IsNotExist(err) {
			c.JSON(404, gin.H{"error": "file not found"})
		} else {
			c.JSON(500, gin.H{"error": fmt.Sprintf("failed to read file: %v", err)})
		}
		return
	}
	c.FileAttachment(full, name)
}

// Upload auth file: multipart or raw JSON with ?name=
func (h *Handler) UploadAuthFile(c *gin.Context) {
	if h.authManager == nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": "core auth manager unavailable"})
		return
	}
	ctx := c.Request.Context()
	if c.Request != nil && c.Request.Body != nil && c.Writer != nil {
		c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, bodyutil.AuthFileBodyLimit+(64<<10))
	}
	contentType := strings.ToLower(strings.TrimSpace(c.GetHeader("Content-Type")))
	if strings.HasPrefix(contentType, "multipart/form-data") {
		file, err := c.FormFile("file")
		if err != nil {
			if bodyutil.IsTooLarge(err) {
				c.JSON(http.StatusRequestEntityTooLarge, gin.H{"error": "file too large"})
				return
			}
			c.JSON(http.StatusBadRequest, gin.H{"error": "file required"})
			return
		}
		if file.Size > bodyutil.AuthFileBodyLimit {
			c.JSON(http.StatusRequestEntityTooLarge, gin.H{"error": "file too large"})
			return
		}
		name, errValidate := managementauthfiles.ValidateUploadedFileName(file.Filename)
		if errValidate != nil {
			c.JSON(400, gin.H{"error": errValidate.Error()})
			return
		}
		dst := managementauthfiles.FilePath(h.cfg.AuthDir, name)
		if errSave := c.SaveUploadedFile(file, dst); errSave != nil {
			c.JSON(500, gin.H{"error": fmt.Sprintf("failed to save file: %v", errSave)})
			return
		}
		data, errRead := os.ReadFile(dst)
		if errRead != nil {
			c.JSON(500, gin.H{"error": fmt.Sprintf("failed to read saved file: %v", errRead)})
			return
		}
		if errReg := h.registerAuthFromFile(ctx, dst, data); errReg != nil {
			c.JSON(500, gin.H{"error": errReg.Error()})
			return
		}
		if errPersist := h.persistAuthFileChange(ctx, "Update auth "+name, dst); errPersist != nil {
			c.JSON(500, gin.H{"error": errPersist.Error()})
			return
		}
		c.JSON(200, gin.H{"status": "ok"})
		return
	}
	name, errValidate := managementauthfiles.ValidateFileQueryName(c.Query("name"), true)
	if errValidate != nil {
		c.JSON(400, gin.H{"error": errValidate.Error()})
		return
	}
	data, err := bodyutil.ReadRequestBody(c, bodyutil.AuthFileBodyLimit)
	if err != nil {
		if bodyutil.IsTooLarge(err) {
			c.JSON(http.StatusRequestEntityTooLarge, gin.H{"error": "request body too large"})
			return
		}
		c.JSON(400, gin.H{"error": "failed to read body"})
		return
	}
	dst := managementauthfiles.FilePath(h.cfg.AuthDir, name)
	if errWrite := os.WriteFile(dst, data, 0o600); errWrite != nil {
		c.JSON(500, gin.H{"error": fmt.Sprintf("failed to write file: %v", errWrite)})
		return
	}
	if err = h.registerAuthFromFile(ctx, dst, data); err != nil {
		c.JSON(500, gin.H{"error": err.Error()})
		return
	}
	if errPersist := h.persistAuthFileChange(ctx, "Update auth "+filepath.Base(name), dst); errPersist != nil {
		c.JSON(500, gin.H{"error": errPersist.Error()})
		return
	}
	c.JSON(200, gin.H{"status": "ok"})
}

// Delete auth files: single by name or all
func (h *Handler) DeleteAuthFile(c *gin.Context) {
	if h.authManager == nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": "core auth manager unavailable"})
		return
	}
	ctx := c.Request.Context()
	service := managementauthfiles.DeleteService{
		AuthDir:        h.cfg.AuthDir,
		Manager:        h.authManager,
		Repository:     h.authFileRepository(),
		RemoveChannels: h.removeChannelReferences,
	}
	if managementauthfiles.IsDeleteAllValue(c.Query("all")) {
		result, err := service.DeleteAll(ctx)
		if err != nil {
			c.JSON(500, gin.H{"error": err.Error()})
			return
		}
		c.JSON(200, gin.H{"status": "ok", "deleted": result.Deleted})
		return
	}
	name, errValidate := managementauthfiles.ValidateFileQueryName(c.Query("name"), false)
	if errValidate != nil {
		c.JSON(400, gin.H{"error": errValidate.Error()})
		return
	}
	if _, err := service.DeleteOne(ctx, name); err != nil {
		if errors.Is(err, managementauthfiles.ErrAuthFileNotFound) {
			c.JSON(404, gin.H{"error": "file not found"})
		} else {
			c.JSON(500, gin.H{"error": err.Error()})
		}
		return
	}
	c.JSON(200, gin.H{"status": "ok"})
}

func (h *Handler) findAuthByNameOrID(name string) *coreauth.Auth {
	if h == nil {
		return nil
	}
	return managementauthfiles.FindByNameOrID(h.authManager, name)
}

func (h *Handler) registerAuthFromFile(ctx context.Context, path string, data []byte) error {
	if h.authManager == nil {
		return nil
	}
	authDir := ""
	if h.cfg != nil {
		authDir = h.cfg.AuthDir
	}
	return managementauthfiles.Registrar{
		Manager: h.authManager,
		AuthDir: authDir,
	}.RegisterFile(ctx, path, data)
}

// PatchAuthFileStatus toggles the disabled state of an auth file
func (h *Handler) PatchAuthFileStatus(c *gin.Context) {
	if h.authManager == nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": "core auth manager unavailable"})
		return
	}

	var req struct {
		Name     string `json:"name"`
		Disabled *bool  `json:"disabled"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid request body"})
		return
	}

	name := strings.TrimSpace(req.Name)
	if name == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "name is required"})
		return
	}
	if req.Disabled == nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "disabled is required"})
		return
	}

	ctx := c.Request.Context()

	targetAuth := h.findAuthByNameOrID(name)
	if targetAuth == nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "auth file not found"})
		return
	}

	if errPatch := managementauthfiles.ApplyStatusPatch(targetAuth, *req.Disabled, time.Now()); errPatch != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": errPatch.Error()})
		return
	}

	if _, err := h.authManager.Update(ctx, targetAuth); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": fmt.Sprintf("failed to update auth: %v", err)})
		return
	}

	c.JSON(http.StatusOK, gin.H{"status": "ok", "disabled": *req.Disabled})
}

// PatchAuthFileFields updates editable fields of an auth file.
func (h *Handler) PatchAuthFileFields(c *gin.Context) {
	if h.authManager == nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": "core auth manager unavailable"})
		return
	}

	var req managementauthfiles.FieldPatch
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid request body"})
		return
	}

	name := strings.TrimSpace(req.Name)
	if name == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "name is required"})
		return
	}

	ctx := c.Request.Context()

	targetAuth := h.findAuthByNameOrID(name)
	if targetAuth == nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "auth file not found"})
		return
	}

	patchResult, errPatch := managementauthfiles.ApplyFieldPatch(targetAuth, req, managementauthfiles.FieldPatchOptions{
		ValidateLabel: h.validateAuthChannelName,
	})
	if errPatch != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": errPatch.Error()})
		return
	}

	if _, err := h.authManager.Update(ctx, targetAuth); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": fmt.Sprintf("failed to update auth: %v", err)})
		return
	}
	if path := strings.TrimSpace(managementauthfiles.Attribute(targetAuth, "path")); path != "" {
		if err := h.persistAuthFileChange(ctx, "Update auth "+targetAuth.FileName, path); err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
			return
		}
	}
	if len(patchResult.OldChannelIdentifiers) > 0 {
		if err := h.renameChannelReferences(patchResult.OldChannelIdentifiers, patchResult.NewChannelLabel); err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
			return
		}
	}

	c.JSON(http.StatusOK, gin.H{"status": "ok"})
}

func (h *Handler) authFileRepository() managementauthfiles.Repository {
	if h == nil {
		return managementauthfiles.Repository{}
	}
	store := h.tokenStore
	if store == nil {
		store = sdkAuth.GetTokenStore()
		h.tokenStore = store
	}
	baseDir := ""
	if h.cfg != nil {
		baseDir = h.cfg.AuthDir
	}
	return managementauthfiles.Repository{
		Store:        store,
		BaseDir:      baseDir,
		PostAuthHook: h.postAuthHook,
	}
}

func (h *Handler) persistAuthFileChange(ctx context.Context, message string, paths ...string) error {
	return h.authFileRepository().PersistChange(ctx, message, paths...)
}

func (h *Handler) saveTokenRecord(ctx context.Context, record *coreauth.Auth) (string, error) {
	return h.authFileRepository().Save(ctx, record)
}

func (h *Handler) RequestAnthropicToken(c *gin.Context) {
	ctx := detachedAuthContext(c)

	result, err := claudeprovider.StartOAuthLogin(ctx, claudeprovider.OAuthLoginOptions{
		Config:                h.cfg,
		WebUI:                 isWebUIRequest(c),
		PreferredCallbackPort: anthropicCallbackPort,
		CallbackTarget:        h.managementCallbackURL,
		WaitCallback:          WaitOAuthCallbackFile,
		CallbackWaitTimeout:   oauthCallbackWaitTimeout,
		SaveRecord:            h.saveTokenRecord,
		Sessions: claudeprovider.SessionCallbacks{
			Register:         RegisterOAuthSession,
			SetError:         SetOAuthSessionError,
			Complete:         CompleteOAuthSession,
			CompleteProvider: CompleteOAuthSessionsByProvider,
		},
	})
	if err != nil {
		switch {
		case errors.Is(err, claudeprovider.ErrPKCEGeneration):
			log.Errorf("Failed to generate PKCE codes: %v", err)
			c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to generate PKCE codes"})
		case errors.Is(err, claudeprovider.ErrStateGeneration):
			log.Errorf("Failed to generate state parameter: %v", err)
			c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to generate state parameter"})
		case errors.Is(err, claudeprovider.ErrAuthURL):
			log.Errorf("Failed to generate authorization URL: %v", err)
			c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to generate authorization url"})
		default:
			log.WithError(err).Error("failed to start anthropic oauth flow")
			c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to generate authorization url"})
		}
		return
	}

	c.JSON(200, gin.H{"status": "ok", "url": result.AuthURL, "state": result.State})
}

func (h *Handler) RequestGeminiCLIToken(c *gin.Context) {
	ctx := detachedAuthContext(c)

	result, err := geminicli.StartOAuthLogin(ctx, geminicli.OAuthLoginOptions{
		Config:              h.cfg,
		ProjectID:           c.Query("project_id"),
		WebUI:               isWebUIRequest(c),
		CallbackPort:        geminiCallbackPort,
		CallbackTarget:      h.managementCallbackURL,
		WaitCallback:        WaitOAuthCallbackFile,
		CallbackWaitTimeout: oauthCallbackWaitTimeout,
		SaveRecord:          h.saveTokenRecord,
		Sessions: geminicli.SessionCallbacks{
			Register:         RegisterOAuthSession,
			SetError:         SetOAuthSessionError,
			Complete:         CompleteOAuthSession,
			CompleteProvider: CompleteOAuthSessionsByProvider,
		},
	})
	if err != nil {
		switch {
		case errors.Is(err, geminicli.ErrOAuthClientIDMissing):
			c.JSON(http.StatusBadRequest, gin.H{"error": "gemini oauth client-id not configured"})
		case errors.Is(err, geminicli.ErrStateGeneration):
			c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to generate state parameter"})
		case errors.Is(err, geminicli.ErrCallbackUnavailable):
			log.WithError(err).Error("failed to compute gemini callback target")
			c.JSON(http.StatusInternalServerError, gin.H{"error": "callback server unavailable"})
		case errors.Is(err, geminicli.ErrCallbackStart):
			log.WithError(err).Error("failed to start gemini callback forwarder")
			c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to start callback server"})
		default:
			log.WithError(err).Error("failed to start gemini oauth flow")
			c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to start oauth flow"})
		}
		return
	}

	c.JSON(200, gin.H{"status": "ok", "url": result.AuthURL, "state": result.State})
}

func (h *Handler) RequestCodexToken(c *gin.Context) {
	ctx := detachedAuthContext(c)

	result, err := codexprovider.StartOAuthLogin(ctx, codexprovider.OAuthLoginOptions{
		Config:              h.cfg,
		WebUI:               isWebUIRequest(c),
		CallbackPort:        codexCallbackPort,
		CallbackTarget:      h.managementCallbackURL,
		WaitCallback:        WaitOAuthCallbackFile,
		CallbackWaitTimeout: oauthCallbackWaitTimeout,
		SaveRecord:          h.saveTokenRecord,
		Sessions: codexprovider.SessionCallbacks{
			Register:         RegisterOAuthSession,
			SetError:         SetOAuthSessionError,
			Complete:         CompleteOAuthSession,
			CompleteProvider: CompleteOAuthSessionsByProvider,
		},
	})
	if err != nil {
		switch {
		case errors.Is(err, codexprovider.ErrPKCEGeneration):
			log.Errorf("Failed to generate PKCE codes: %v", err)
			c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to generate PKCE codes"})
		case errors.Is(err, codexprovider.ErrStateGeneration):
			log.Errorf("Failed to generate state parameter: %v", err)
			c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to generate state parameter"})
		case errors.Is(err, codexprovider.ErrAuthURL):
			log.Errorf("Failed to generate authorization URL: %v", err)
			c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to generate authorization url"})
		default:
			log.WithError(err).Error("failed to start codex oauth flow")
			c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to generate authorization url"})
		}
		return
	}

	c.JSON(200, gin.H{"status": "ok", "url": result.AuthURL, "state": result.State})
}

func (h *Handler) RequestAntigravityToken(c *gin.Context) {
	ctx := detachedAuthContext(c)

	result, err := antigravityprovider.StartOAuthLogin(ctx, antigravityprovider.OAuthLoginOptions{
		Config:              h.cfg,
		WebUI:               isWebUIRequest(c),
		CallbackTarget:      h.managementCallbackURL,
		WaitCallback:        WaitOAuthCallbackFile,
		CallbackWaitTimeout: oauthCallbackWaitTimeout,
		SaveRecord:          h.saveTokenRecord,
		Sessions: antigravityprovider.SessionCallbacks{
			Register:         RegisterOAuthSession,
			SetError:         SetOAuthSessionError,
			Complete:         CompleteOAuthSession,
			CompleteProvider: CompleteOAuthSessionsByProvider,
		},
	})
	if err != nil {
		switch {
		case errors.Is(err, antigravityprovider.ErrStateGeneration):
			log.Errorf("Failed to generate state parameter: %v", err)
			c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to generate state parameter"})
		case errors.Is(err, antigravityprovider.ErrCallbackUnavailable):
			log.WithError(err).Error("failed to compute antigravity callback target")
			c.JSON(http.StatusInternalServerError, gin.H{"error": "callback server unavailable"})
		case errors.Is(err, antigravityprovider.ErrCallbackStart):
			log.WithError(err).Error("failed to start antigravity callback forwarder")
			c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to start callback server"})
		case errors.Is(err, antigravityprovider.ErrOAuthClientIDMissing):
			c.JSON(http.StatusBadRequest, gin.H{"error": "antigravity oauth client-id not configured"})
		default:
			log.WithError(err).Error("failed to start antigravity oauth flow")
			c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to start oauth flow"})
		}
		return
	}

	c.JSON(200, gin.H{"status": "ok", "url": result.AuthURL, "state": result.State})
}

func (h *Handler) RequestQwenToken(c *gin.Context) {
	ctx := detachedAuthContext(c)

	result, err := qwenprovider.StartDeviceLogin(ctx, qwenprovider.DeviceLoginOptions{
		Config:     h.cfg,
		SaveRecord: h.saveTokenRecord,
		Sessions: qwenprovider.SessionCallbacks{
			Register: RegisterOAuthSession,
			SetError: SetOAuthSessionError,
			Complete: CompleteOAuthSession,
		},
	})
	if err != nil {
		log.Errorf("Failed to generate authorization URL: %v", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to generate authorization url"})
		return
	}

	c.JSON(200, gin.H{"status": "ok", "url": result.AuthURL, "state": result.State})
}

func (h *Handler) RequestKimiToken(c *gin.Context) {
	ctx := detachedAuthContext(c)

	result, err := kimiprovider.StartDeviceLogin(ctx, kimiprovider.DeviceLoginOptions{
		Config:     h.cfg,
		SaveRecord: h.saveTokenRecord,
		Sessions: kimiprovider.SessionCallbacks{
			Register:         RegisterOAuthSession,
			SetError:         SetOAuthSessionError,
			Complete:         CompleteOAuthSession,
			CompleteProvider: CompleteOAuthSessionsByProvider,
		},
	})
	if err != nil {
		log.Errorf("Failed to generate authorization URL: %v", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to generate authorization url"})
		return
	}

	c.JSON(200, gin.H{"status": "ok", "url": result.AuthURL, "state": result.State})
}

func (h *Handler) RequestIFlowToken(c *gin.Context) {
	ctx := detachedAuthContext(c)

	result, err := iflowprovider.StartOAuthLogin(ctx, iflowprovider.OAuthLoginOptions{
		Config:              h.cfg,
		WebUI:               isWebUIRequest(c),
		CallbackTarget:      h.managementCallbackURL,
		WaitCallback:        WaitOAuthCallbackFile,
		CallbackWaitTimeout: oauthCallbackWaitTimeout,
		SaveRecord:          h.saveTokenRecord,
		Sessions: iflowprovider.SessionCallbacks{
			Register:         RegisterOAuthSession,
			SetError:         SetOAuthSessionError,
			Complete:         CompleteOAuthSession,
			CompleteProvider: CompleteOAuthSessionsByProvider,
		},
	})
	if err != nil {
		switch {
		case errors.Is(err, iflowprovider.ErrCallbackUnavailable):
			log.WithError(err).Error("failed to compute iflow callback target")
			c.JSON(http.StatusInternalServerError, gin.H{"status": "error", "error": "callback server unavailable"})
		case errors.Is(err, iflowprovider.ErrCallbackStart):
			log.WithError(err).Error("failed to start iflow callback forwarder")
			c.JSON(http.StatusInternalServerError, gin.H{"status": "error", "error": "failed to start callback server"})
		default:
			log.WithError(err).Error("failed to start iflow oauth flow")
			c.JSON(http.StatusInternalServerError, gin.H{"status": "error", "error": "failed to start oauth flow"})
		}
		return
	}

	c.JSON(http.StatusOK, gin.H{"status": "ok", "url": result.AuthURL, "state": result.State})
}

func (h *Handler) RequestIFlowCookieToken(c *gin.Context) {
	ctx := requestAuthContext(c)

	var payload struct {
		Cookie string `json:"cookie"`
	}
	if err := c.ShouldBindJSON(&payload); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"status": "error", "error": "cookie is required"})
		return
	}

	result, err := iflowprovider.AuthenticateCookie(ctx, payload.Cookie, iflowprovider.CookieLoginOptions{
		Config:     h.cfg,
		SaveRecord: h.saveTokenRecord,
	})
	if err != nil {
		var duplicate iflowprovider.DuplicateBXAuthError
		switch {
		case errors.Is(err, iflowprovider.ErrCookieRequired):
			c.JSON(http.StatusBadRequest, gin.H{"status": "error", "error": "cookie is required"})
		case errors.Is(err, iflowprovider.ErrDuplicateCheck):
			c.JSON(http.StatusInternalServerError, gin.H{"status": "error", "error": "failed to check duplicate"})
		case errors.As(err, &duplicate):
			c.JSON(http.StatusConflict, gin.H{"status": "error", "error": "duplicate BXAuth found", "existing_file": duplicate.ExistingFileName()})
		case errors.Is(err, iflowprovider.ErrExtractEmail):
			c.JSON(http.StatusBadRequest, gin.H{"status": "error", "error": "failed to extract email from token"})
		case errors.Is(err, iflowprovider.ErrSaveTokens):
			c.JSON(http.StatusInternalServerError, gin.H{"status": "error", "error": "failed to save authentication tokens"})
		default:
			c.JSON(http.StatusBadRequest, gin.H{"status": "error", "error": err.Error()})
		}
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"status":     "ok",
		"saved_path": result.SavedPath,
		"email":      result.Email,
		"expired":    result.Expired,
		"type":       result.Type,
	})
}

func (h *Handler) GetAuthStatus(c *gin.Context) {
	state := strings.TrimSpace(c.Query("state"))
	if state == "" {
		c.JSON(http.StatusOK, gin.H{"status": "ok"})
		return
	}
	if err := ValidateOAuthState(state); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"status": "error", "error": "invalid state"})
		return
	}

	_, status, ok := GetOAuthSession(state)
	if !ok {
		c.JSON(http.StatusOK, gin.H{"status": "ok"})
		return
	}
	if status == oauthSessionStatusCompleted {
		c.JSON(http.StatusOK, gin.H{"status": "ok"})
		return
	}
	if status != "" {
		c.JSON(http.StatusOK, gin.H{"status": "error", "error": status})
		return
	}
	c.JSON(http.StatusOK, gin.H{"status": "wait"})
}

// PopulateAuthContext extracts request info and adds it to the context
func PopulateAuthContext(ctx context.Context, c *gin.Context) context.Context {
	if ctx == nil {
		ctx = context.Background()
	}
	if c == nil || c.Request == nil {
		return ctx
	}
	info := &coreauth.RequestInfo{
		Query:   c.Request.URL.Query(),
		Headers: c.Request.Header,
	}
	return coreauth.WithRequestInfo(ctx, info)
}

func requestAuthContext(c *gin.Context) context.Context {
	if c != nil && c.Request != nil {
		if reqCtx := c.Request.Context(); reqCtx != nil {
			return PopulateAuthContext(reqCtx, c)
		}
	}
	return PopulateAuthContext(context.Background(), c)
}

func detachedAuthContext(c *gin.Context) context.Context {
	if c != nil && c.Request != nil {
		if reqCtx := c.Request.Context(); reqCtx != nil {
			return PopulateAuthContext(context.WithoutCancel(reqCtx), c)
		}
	}
	return PopulateAuthContext(context.Background(), c)
}
