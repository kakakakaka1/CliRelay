package modelcatalog

import (
	"context"
	"testing"

	"github.com/router-for-me/CLIProxyAPI/v6/internal/config"
	"github.com/router-for-me/CLIProxyAPI/v6/internal/registry"
	coreauth "github.com/router-for-me/CLIProxyAPI/v6/sdk/cliproxy/auth"
)

func TestConfiguredAvailabilityIncludesModelSources(t *testing.T) {
	const modelID = "source-test-model"
	const codexClientID = "source-test-codex"
	const openCodeClientID = "source-test-opencode"

	modelRegistry := registry.GetGlobalRegistry()
	modelRegistry.UnregisterClient(codexClientID)
	modelRegistry.UnregisterClient(openCodeClientID)
	t.Cleanup(func() {
		modelRegistry.UnregisterClient(codexClientID)
		modelRegistry.UnregisterClient(openCodeClientID)
	})

	modelRegistry.RegisterClient(codexClientID, "codex", []*registry.ModelInfo{{ID: modelID, Object: "model", OwnedBy: "openai"}})
	modelRegistry.RegisterClient(openCodeClientID, "opencode-go", []*registry.ModelInfo{{ID: modelID, Object: "model", OwnedBy: "opencode"}})

	manager := coreauth.NewManager(nil, nil, nil)
	if _, err := manager.Register(context.Background(), &coreauth.Auth{ID: codexClientID, Provider: "codex", Label: "Codex Pro", Status: coreauth.StatusActive}); err != nil {
		t.Fatalf("register codex auth: %v", err)
	}
	if _, err := manager.Register(context.Background(), &coreauth.Auth{ID: openCodeClientID, Provider: "opencode-go", Label: "OpenCode Go", Status: coreauth.StatusActive}); err != nil {
		t.Fatalf("register opencode auth: %v", err)
	}

	result := New(&config.Config{}, manager).ConfiguredAvailability("", "")
	data, ok := result["data"].([]map[string]any)
	if !ok {
		t.Fatalf("data = %#v, want []map[string]any", result["data"])
	}

	var sources []map[string]any
	for _, item := range data {
		if item["id"] == modelID {
			sources, _ = item["sources"].([]map[string]any)
			break
		}
	}
	if len(sources) != 2 {
		t.Fatalf("sources = %#v, want two sources", sources)
	}
	labels := map[string]bool{}
	for _, source := range sources {
		labels[source["label"].(string)] = true
	}
	if !labels["codex · Codex Pro"] || !labels["opencode-go · OpenCode Go"] {
		t.Fatalf("source labels = %#v", labels)
	}
}
