package executor

import (
	"context"
	"strings"

	cliproxyauth "github.com/router-for-me/CLIProxyAPI/v6/sdk/cliproxy/auth"
	cliproxyexecutor "github.com/router-for-me/CLIProxyAPI/v6/sdk/cliproxy/executor"
)

// The upstream requires a conversation ID. Forward the client's explicit ID;
// neither a shared credential ID nor a request ID identifies a conversation.
func opencodeGoAuthWithSessionHeader(ctx context.Context, auth *cliproxyauth.Auth, opts cliproxyexecutor.Options) *cliproxyauth.Auth {
	session := strings.TrimSpace(opts.Headers.Get("X-Opencode-Session"))
	if session == "" {
		if ginCtx := ginContextFrom(ctx); ginCtx != nil && ginCtx.Request != nil {
			session = strings.TrimSpace(ginCtx.Request.Header.Get("X-Opencode-Session"))
		}
	}
	if auth == nil || session == "" {
		return auth
	}
	// Credentials are shared across concurrent requests; request headers belong
	// only to this execution and must never be persisted onto the account.
	scoped := auth.Clone()
	if scoped.Attributes == nil {
		scoped.Attributes = make(map[string]string)
	}
	scoped.Attributes["header:X-Opencode-Session"] = session
	return scoped
}
