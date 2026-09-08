package config

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

func TestCodexAlphaSearchConfigRoundTrip(t *testing.T) {
	for _, enabled := range []bool{false, true} {
		input := `{"api-key":"test-codex","alpha-search":false}`
		if enabled {
			input = `{"api-key":"test-codex","alpha-search":true}`
		}
		var key CodexKey
		if err := json.Unmarshal([]byte(input), &key); err != nil {
			t.Fatal(err)
		}
		encoded, err := yaml.Marshal(key)
		if err != nil {
			t.Fatal(err)
		}
		if strings.Contains(string(encoded), "alpha-search: true") != enabled {
			t.Fatalf("YAML lost opt-in: %s", encoded)
		}
		var restored CodexKey
		if err := yaml.Unmarshal(encoded, &restored); err != nil {
			t.Fatal(err)
		}
		encoded, err = json.Marshal(restored)
		if err != nil {
			t.Fatal(err)
		}
		var fields map[string]interface{}
		if err := json.Unmarshal(encoded, &fields); err != nil {
			t.Fatal(err)
		}
		if got, _ := fields["alpha-search"].(bool); got != enabled {
			t.Fatalf("JSON lost opt-in: %s", encoded)
		}
	}
}

func TestCodexAlphaSearchYAMLSaveCanDisable(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.yaml")
	if err := os.WriteFile(path, []byte("# operator config\ncodex-api-key:\n  - api-key: test-codex\n    alpha-search: true\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg, err := LoadConfig(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(cfg.CodexKey) != 1 || !cfg.CodexKey[0].AlphaSearch {
		t.Fatal("LoadConfig lost explicit opt-in")
	}
	cfg.CodexKey[0].AlphaSearch = false
	if err := SaveConfigPreserveComments(path, cfg); err != nil {
		t.Fatal(err)
	}
	restored, err := LoadConfig(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(restored.CodexKey) != 1 || restored.CodexKey[0].AlphaSearch {
		t.Fatal("YAML save retained stale alpha-search opt-in")
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), "# operator config") {
		t.Fatal("YAML save lost operator comment")
	}
}
