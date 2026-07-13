package modelcatalog

import (
	"github.com/router-for-me/CLIProxyAPI/v6/internal/config"
	coreauth "github.com/router-for-me/CLIProxyAPI/v6/sdk/cliproxy/auth"
)

type Service struct {
	tenantID    string
	cfg         *config.Config
	authManager *coreauth.Manager
}

func New(cfg *config.Config, authManager *coreauth.Manager) *Service {
	return NewForTenant("", cfg, authManager)
}
func NewForTenant(tenantID string, cfg *config.Config, authManager *coreauth.Manager) *Service {
	return &Service{tenantID: tenantID, cfg: cfg, authManager: authManager}
}
