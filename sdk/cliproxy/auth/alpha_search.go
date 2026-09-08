package auth

import "strings"

// SupportsCodexAlphaSearch keeps this independent protocol off generic Responses
// channels. API keys require an operator opt-in; OAuth uses the Codex endpoint.
func SupportsCodexAlphaSearch(a *Auth) bool {
	if a == nil || a.Provider != "codex" {
		return false
	}
	if strings.TrimSpace(a.Attributes["api_key"]) != "" {
		return a.Attributes["alpha_search"] == "true" && strings.TrimSpace(a.Attributes["base_url"]) != ""
	}
	token, _ := a.Metadata["access_token"].(string)
	return strings.TrimSpace(token) != ""
}
