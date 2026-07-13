package cliproxy

import (
	"strings"

	coreauth "github.com/router-for-me/CLIProxyAPI/v6/sdk/cliproxy/auth"
	serviceapp "github.com/router-for-me/CLIProxyAPI/v6/sdkbridge/service"
)

func (s *Service) ensureExecutorsForAuth(a *coreauth.Auth) {
	s.ensureExecutorsForAuthWithMode(a, false)
}

func (s *Service) ensureExecutorsForAuthWithMode(a *coreauth.Auth, forceReplace bool) {
	if s == nil {
		return
	}
	serviceapp.RegisterExecutorForAuth(s.coreManager, s.cfg, a, forceReplace, s.wsGateway)
}

// rebindExecutors refreshes provider executors so they observe the latest configuration.
func (s *Service) rebindExecutors() {
	if s == nil || s.coreManager == nil {
		return
	}
	rebound := make(map[string]struct{})
	for _, auth := range s.coreManager.List() {
		if auth == nil {
			continue
		}
		providerKey := strings.ToLower(strings.TrimSpace(auth.Provider))
		if auth.Attributes != nil && strings.TrimSpace(auth.Attributes["compat_name"]) != "" {
			if configured := strings.TrimSpace(auth.Attributes["provider_key"]); configured != "" {
				providerKey = strings.ToLower(configured)
			}
		}
		key := coreauth.NormalizedTenantID(auth.TenantID) + "\x00" + providerKey
		if providerKey != "aistudio" {
			if _, exists := rebound[key]; exists {
				continue
			}
			rebound[key] = struct{}{}
		}
		s.ensureExecutorsForAuthWithMode(auth, true)
	}
}
