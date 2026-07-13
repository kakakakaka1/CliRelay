package management

import (
	"time"

	managementauthfiles "github.com/router-for-me/CLIProxyAPI/v6/internal/management/authfiles"
	oauthsession "github.com/router-for-me/CLIProxyAPI/v6/internal/management/oauth/session"
)

const (
	oauthSessionTTL             = oauthsession.DefaultTTL
	maxOAuthStateLength         = oauthsession.MaxStateLength
	oauthSessionStatusCompleted = oauthsession.StatusCompleted
)

var (
	errInvalidOAuthState      = oauthsession.ErrInvalidState
	errUnsupportedOAuthFlow   = oauthsession.ErrUnsupportedFlow
	errOAuthSessionNotPending = oauthsession.ErrNotPending
)

var oauthSessions = newOAuthSessionStore(oauthSessionTTL)

func newOAuthSessionStore(ttl time.Duration) *oauthsession.Store {
	return oauthsession.NewStore(ttl)
}

func RegisterOAuthSession(state, provider string) { oauthSessions.Register(state, provider) }

func RegisterOAuthSessionForTenant(state, provider, tenantID string) {
	oauthSessions.RegisterTenant(state, provider, tenantID)
}

func SetOAuthSessionError(state, message string) { oauthSessions.SetError(state, message) }

func CompleteOAuthSession(state string) { oauthSessions.Complete(state) }

func CompleteOAuthSessionsByProvider(provider string) int {
	return oauthSessions.CompleteProvider(provider)
}

func CompleteOAuthSessionsByProviderForTenant(provider, tenantID string) int {
	return oauthSessions.CompleteProviderTenant(provider, tenantID)
}

func GetOAuthSession(state string) (provider string, status string, ok bool) {
	session, ok := oauthSessions.Get(state)
	if !ok {
		return "", "", false
	}
	return session.Provider, session.Status, true
}

func GetOAuthSessionWithTenant(state string) (provider, tenantID, status string, ok bool) {
	session, ok := oauthSessions.Get(state)
	if !ok {
		return "", "", "", false
	}
	return session.Provider, session.TenantID, session.Status, true
}

func IsOAuthSessionPending(state, provider string) bool {
	return oauthSessions.IsPending(state, provider)
}

func ValidateOAuthState(state string) error {
	return oauthsession.ValidateState(state)
}

func NormalizeOAuthProvider(provider string) (string, error) {
	return oauthsession.NormalizeProvider(provider)
}

func WriteOAuthCallbackFile(authDir, provider, state, code, errorMessage string) (string, error) {
	return oauthsession.WriteCallbackFile(authDir, provider, state, code, errorMessage)
}

func WriteOAuthCallbackFileForPendingSession(authDir, provider, state, code, errorMessage string) (string, error) {
	if _, tenantID, _, ok := GetOAuthSessionWithTenant(state); ok && tenantID != "" {
		authDir = managementauthfiles.TenantAuthDir(authDir, tenantID)
	}
	return oauthSessions.WriteCallbackFileForPending(authDir, provider, state, code, errorMessage)
}

func WaitOAuthCallbackFile(authDir, provider, state string, timeout time.Duration) (map[string]string, error) {
	return oauthSessions.WaitCallbackFile(authDir, provider, state, timeout, 500*time.Millisecond)
}
