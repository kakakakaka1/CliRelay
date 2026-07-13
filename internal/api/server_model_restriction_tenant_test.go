package api

import (
	"testing"

	"github.com/router-for-me/CLIProxyAPI/v6/internal/config"
	"github.com/router-for-me/CLIProxyAPI/v6/internal/identity"
)

func TestScopedRoutingAllowedModelsUsesSystemCfgWhenNoDB(t *testing.T) {
	server := &Server{cfg: &config.Config{
		Routing: config.RoutingConfig{
			IncludeDefaultGroup: true,
			ChannelGroups: []config.RoutingChannelGroup{
				{Name: "default", AllowedModels: []string{"gpt-5.5"}},
			},
		},
	}}
	allowed := server.scopedRoutingAllowedModelsForTenant(identity.SystemTenantID, "", nil)
	if len(allowed) != 1 || allowed[0] != "gpt-5.5" {
		t.Fatalf("allowed = %#v, want [gpt-5.5]", allowed)
	}
	if server.modelAllowedByScopedRoutingGroupsForTenant(identity.SystemTenantID, "minimax-m2.7", "", nil) {
		t.Fatal("expected minimax-m2.7 to be forbidden by default group allowed-models")
	}
	if !server.modelAllowedByScopedRoutingGroupsForTenant(identity.SystemTenantID, "gpt-5.5", "", nil) {
		t.Fatal("expected gpt-5.5 to be allowed")
	}
}

func TestScopedRoutingAllowedModelsDoesNotUseSystemCfgForOtherTenant(t *testing.T) {
	server := &Server{cfg: &config.Config{
		Routing: config.RoutingConfig{
			IncludeDefaultGroup: true,
			ChannelGroups: []config.RoutingChannelGroup{
				{Name: "default", AllowedModels: []string{"gpt-5.5"}},
			},
		},
	}}
	// Non-system tenant without a DB routing row must not inherit system allowed-models.
	other := "cccccccc-dddd-eeee-ffff-000000000001"
	allowed := server.scopedRoutingAllowedModelsForTenant(other, "", nil)
	if allowed != nil {
		t.Fatalf("allowed = %#v, want nil (no tenant routing row)", allowed)
	}
	if !server.modelAllowedByScopedRoutingGroupsForTenant(other, "minimax-m2.7", "", nil) {
		t.Fatal("expected unrestricted when tenant has no routing row")
	}
}
