package executor

import (
	"bytes"
	"net/http"
	"testing"

	cliproxyauth "github.com/router-for-me/CLIProxyAPI/v6/sdk/cliproxy/auth"
	"github.com/tidwall/gjson"
)

func TestMaybeEnsureCodexImageGenerationToolInjectsWhenEnabled(t *testing.T) {
	auth := &cliproxyauth.Auth{
		Provider: "codex",
		Metadata: map[string]any{
			"codex_image_generation_bridge": true,
		},
	}
	body := []byte(`{"model":"gpt-5.4","tools":[{"type":"function","name":"shell"}]}`)
	out := maybeEnsureCodexImageGenerationTool(body, auth, "gpt-5.4", nil)
	if got := gjson.GetBytes(out, "tools.#").Int(); got != 2 {
		t.Fatalf("tools count = %d, want 2; body=%s", got, out)
	}
	if got := gjson.GetBytes(out, "tools.1.type").String(); got != "image_generation" {
		t.Fatalf("tools.1.type = %q, want image_generation; body=%s", got, out)
	}
}

func TestMaybeEnsureCodexImageGenerationToolSkipsWhenDisabled(t *testing.T) {
	auth := &cliproxyauth.Auth{
		Provider: "codex",
		Metadata: map[string]any{
			"codex_image_generation_bridge": false,
		},
	}
	body := []byte(`{"model":"gpt-5.4","tools":[{"type":"function","name":"shell"}]}`)
	out := maybeEnsureCodexImageGenerationTool(body, auth, "gpt-5.4", nil)
	if got := gjson.GetBytes(out, "tools.#").Int(); got != 1 {
		t.Fatalf("tools count = %d, want 1; body=%s", got, out)
	}
}

func TestMaybeEnsureCodexImageGenerationToolSkipsExistingImageGenNamespace(t *testing.T) {
	auth := &cliproxyauth.Auth{
		Provider: "codex",
		Metadata: map[string]any{
			"codex_image_generation_bridge": true,
		},
	}
	body := []byte(`{"model":"gpt-5.4","tools":[{"type":"namespace","name":"image_gen","tools":[{"type":"function","name":"imagegen"}]}]}`)
	out := maybeEnsureCodexImageGenerationTool(body, auth, "gpt-5.4", nil)
	if got := gjson.GetBytes(out, "tools.#").Int(); got != 1 {
		t.Fatalf("tools count = %d, want 1; body=%s", got, out)
	}
}

func TestMaybeEnsureCodexImageGenerationToolSkipsSparkAndLite(t *testing.T) {
	auth := &cliproxyauth.Auth{
		Provider: "codex",
		Metadata: map[string]any{
			"codex_image_generation_bridge": true,
		},
	}
	body := []byte(`{"model":"gpt-5.3-codex-spark"}`)
	out := maybeEnsureCodexImageGenerationTool(body, auth, "gpt-5.3-codex-spark", nil)
	if gjson.GetBytes(out, "tools").Exists() {
		t.Fatalf("spark model should not inject tools; body=%s", out)
	}

	liteBody := []byte(`{"model":"gpt-5.4"}`)
	headers := http.Header{}
	headers.Set("X-OpenAI-Internal-Codex-Responses-Lite", "true")
	out = maybeEnsureCodexImageGenerationTool(liteBody, auth, "gpt-5.4", headers)
	if gjson.GetBytes(out, "tools").Exists() {
		t.Fatalf("responses-lite should not inject tools; body=%s", out)
	}
}

func TestNormalizeCodexImageGenerationCallStatusCompletesGeneratingWithResult(t *testing.T) {
	in := []byte(`data: {"type":"response.output_item.done","item":{"id":"ig_1","type":"image_generation_call","status":"generating","result":"iVBORw0KGgo="}}`)
	out := normalizeCodexImageGenerationCallStatus(in)
	if got := gjson.GetBytes(bytes.TrimSpace(out[5:]), "item.status").String(); got != "completed" {
		t.Fatalf("item.status = %q, want completed; out=%s", got, out)
	}
	if !bytes.HasPrefix(out, dataTag) {
		t.Fatalf("expected SSE data prefix, got %s", out)
	}
}

func TestNormalizeCodexImageGenerationCallStatusCompletesResponseOutput(t *testing.T) {
	in := []byte(`{"type":"response.completed","response":{"output":[{"type":"image_generation_call","status":"generating","result":"ZmFrZQ=="},{"type":"message","status":"completed"}]}}`)
	out := normalizeCodexImageGenerationCallStatus(in)
	if got := gjson.GetBytes(out, "response.output.0.status").String(); got != "completed" {
		t.Fatalf("output.0.status = %q, want completed; out=%s", got, out)
	}
	if got := gjson.GetBytes(out, "response.output.1.status").String(); got != "completed" {
		t.Fatalf("output.1.status = %q, want completed; out=%s", got, out)
	}
}

func TestNormalizeCodexImageGenerationCallStatusSkipsWithoutResult(t *testing.T) {
	in := []byte(`{"type":"response.output_item.done","item":{"type":"image_generation_call","status":"generating"}}`)
	out := normalizeCodexImageGenerationCallStatus(in)
	if !bytes.Equal(in, out) {
		t.Fatalf("payload changed without result: %s", out)
	}
}
