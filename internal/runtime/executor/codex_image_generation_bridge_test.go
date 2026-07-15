package executor

import (
	"bytes"
	"net/http"
	"strings"
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

func TestMaybeEnsureStripsHostedWhenLocalImageGenPresent(t *testing.T) {
	auth := &cliproxyauth.Auth{
		Provider: "codex",
		Metadata: map[string]any{"codex_image_generation_bridge": true},
	}
	body := []byte(`{"model":"gpt-5.4","tools":[{"type":"image_generation"},{"type":"namespace","name":"image_gen","tools":[{"type":"function","name":"imagegen"}]}],"tool_choice":{"type":"image_generation"}}`)
	out := maybeEnsureCodexImageGenerationTool(body, auth, "gpt-5.4", nil)
	if gjson.GetBytes(out, "tools.#").Int() != 1 {
		t.Fatalf("tools count = %d, want 1; body=%s", gjson.GetBytes(out, "tools.#").Int(), out)
	}
	if got := gjson.GetBytes(out, "tools.0.name").String(); got != "image_gen" {
		t.Fatalf("remaining tool = %q, want image_gen; body=%s", got, out)
	}
	if got := gjson.GetBytes(out, "tool_choice").String(); got != "auto" {
		t.Fatalf("tool_choice = %q, want auto; body=%s", got, out)
	}
}

func TestMaybeEnsureInjectsHostedWhenNoLocalImageGenEvenForDesktop(t *testing.T) {
	auth := &cliproxyauth.Auth{
		Provider: "codex",
		Metadata: map[string]any{"codex_image_generation_bridge": true},
	}
	headers := http.Header{}
	headers.Set("Originator", "Codex Desktop")
	headers.Set("User-Agent", "Codex Desktop/0.144.2")
	body := []byte(`{"model":"gpt-5.4","tools":[{"type":"function","name":"shell"}],"instructions":"base"}`)
	out := maybeEnsureCodexImageGenerationTool(body, auth, "gpt-5.4", headers)
	if got := gjson.GetBytes(out, "tools.1.type").String(); got != "image_generation" {
		t.Fatalf("tools.1.type = %q, want image_generation; body=%s", got, out)
	}
	if !strings.Contains(gjson.GetBytes(out, "instructions").String(), "cliproxy-codex-image-generation") {
		t.Fatalf("instructions missing bridge text; body=%s", out)
	}
}

func TestSynthesizeCodexImageDisplayMessageEvent(t *testing.T) {
	in := []byte(`data: {"type":"response.output_item.done","item":{"id":"ig_1","type":"image_generation_call","status":"completed","result":"iVBORw0KGgo=","output_format":"png","revised_prompt":"cute cat"}}`)
	events := normalizeCodexImageGenerationOutboundEvent(in)
	if len(events) != 2 {
		t.Fatalf("events = %d, want 2", len(events))
	}
	display := bytes.TrimSpace(events[1][len(dataTag):])
	if got := gjson.GetBytes(display, "item.type").String(); got != "message" {
		t.Fatalf("display item.type = %q, want message; %s", got, display)
	}
	if got := gjson.GetBytes(display, "item.role").String(); got != "assistant" {
		t.Fatalf("display role = %q, want assistant", got)
	}
	text := gjson.GetBytes(display, "item.content.0.text").String()
	if !strings.Contains(text, "data:image/png;base64,iVBORw0KGgo=") {
		t.Fatalf("display text missing data url: %s", text)
	}
}

func TestCodexImageStreamRewritesMntDataMarkdownAfterHostedImage(t *testing.T) {
	n := newCodexImageStreamNormalizer()
	ig := []byte(`data: {"type":"response.output_item.done","item":{"id":"ig_1","type":"image_generation_call","status":"completed","result":"iVBORw0KGgo=","output_format":"png"}}`)
	events := normalizeCodexImageGenerationOutboundEventWithState(n, ig)
	if len(events) < 1 {
		t.Fatalf("expected image events, got %d", len(events))
	}
	if len(n.images) != 1 {
		t.Fatalf("cached images = %d, want 1", len(n.images))
	}

	// Desktop-visible path: model writes ChatGPT sandbox markdown.
	textDone := []byte(`data: {"type":"response.output_text.done","item_id":"msg_1","text":"给你画好了：\n\n![小猫](/mnt/data/0.png)\n"}`)
	rewritten := normalizeCodexImageGenerationOutboundEventWithState(n, textDone)
	if len(rewritten) != 1 {
		t.Fatalf("text events = %d, want 1", len(rewritten))
	}
	body := bytes.TrimSpace(rewritten[0][len(dataTag):])
	got := gjson.GetBytes(body, "text").String()
	if !strings.Contains(got, "data:image/png;base64,iVBORw0KGgo=") {
		t.Fatalf("text not rewritten to data url: %s", got)
	}
	if strings.Contains(got, "/mnt/data/") {
		t.Fatalf("text still has /mnt/data: %s", got)
	}
	if !strings.Contains(got, "![小猫](") {
		t.Fatalf("alt text lost: %s", got)
	}

	msgDone := []byte(`data: {"type":"response.output_item.done","item":{"id":"msg_1","type":"message","role":"assistant","status":"completed","content":[{"type":"output_text","text":"给你画好了：\n\n![小猫](/mnt/data/0.png)\n"}]}}`)
	rewritten = normalizeCodexImageGenerationOutboundEventWithState(n, msgDone)
	body = bytes.TrimSpace(rewritten[0][len(dataTag):])
	got = gjson.GetBytes(body, "item.content.0.text").String()
	if !strings.Contains(got, "data:image/png;base64,iVBORw0KGgo=") || strings.Contains(got, "/mnt/data/") {
		t.Fatalf("message item not rewritten: %s", got)
	}
}

func TestCodexImageStreamDoesNotRewriteWithoutHostedImage(t *testing.T) {
	n := newCodexImageStreamNormalizer()
	in := []byte(`data: {"type":"response.output_text.done","text":"see ![x](/mnt/data/0.png)"}`)
	out := normalizeCodexImageGenerationOutboundEventWithState(n, in)
	body := bytes.TrimSpace(out[0][len(dataTag):])
	if got := gjson.GetBytes(body, "text").String(); got != "see ![x](/mnt/data/0.png)" {
		t.Fatalf("rewrote without image result: %s", got)
	}
}

func TestRewriteCodexMntDataImageMarkdownClampsIndex(t *testing.T) {
	images := []codexHostedImage{{Result: "AAA", MIME: "image/png"}}
	got, ok := rewriteCodexMntDataImageMarkdown("![a](/mnt/data/3.png)", images)
	if !ok || !strings.Contains(got, "data:image/png;base64,AAA") {
		t.Fatalf("clamp rewrite failed: ok=%v got=%s", ok, got)
	}
}

func TestCodexImageStreamAppendsDataURLWhenModelOmitsMntPath(t *testing.T) {
	// Real Desktop failure mode after #699: model writes plain text "给你一只小猫。" with no /mnt/data.
	n := newCodexImageStreamNormalizer()
	ig := []byte(`data: {"type":"response.output_item.done","item":{"id":"ig_1","type":"image_generation_call","status":"completed","result":"iVBORw0KGgo=","output_format":"png"}}`)
	_ = normalizeCodexImageGenerationOutboundEventWithState(n, ig)

	textDone := []byte(`data: {"type":"response.output_text.done","item_id":"msg_1","text":"给你一只小猫。\n\n如果你要，我还能继续改。"}`)
	out := normalizeCodexImageGenerationOutboundEventWithState(n, textDone)
	body := bytes.TrimSpace(out[0][len(dataTag):])
	got := gjson.GetBytes(body, "text").String()
	if !strings.Contains(got, "给你一只小猫。") {
		t.Fatalf("original text lost: %s", got)
	}
	if !strings.Contains(got, "data:image/png;base64,iVBORw0KGgo=") {
		t.Fatalf("expected appended data url: %s", got)
	}

	msgDone := []byte(`data: {"type":"response.output_item.done","item":{"id":"msg_1","type":"message","role":"assistant","status":"completed","content":[{"type":"output_text","text":"给你一只小猫。"}]}}`)
	out = normalizeCodexImageGenerationOutboundEventWithState(n, msgDone)
	body = bytes.TrimSpace(out[0][len(dataTag):])
	got = gjson.GetBytes(body, "item.content.0.text").String()
	if !strings.Contains(got, "data:image/png;base64,iVBORw0KGgo=") {
		t.Fatalf("message item missing appended image: %s", got)
	}
}

func TestCodexImageStreamDoesNotDoubleAppendWhenDataURLPresent(t *testing.T) {
	n := newCodexImageStreamNormalizer()
	ig := []byte(`data: {"type":"response.output_item.done","item":{"id":"ig_1","type":"image_generation_call","status":"completed","result":"iVBORw0KGgo=","output_format":"png"}}`)
	_ = normalizeCodexImageGenerationOutboundEventWithState(n, ig)
	in := []byte(`data: {"type":"response.output_text.done","text":"done\n\n![x](data:image/png;base64,iVBORw0KGgo=)"}`)
	out := normalizeCodexImageGenerationOutboundEventWithState(n, in)
	body := bytes.TrimSpace(out[0][len(dataTag):])
	got := gjson.GetBytes(body, "text").String()
	if strings.Count(got, "data:image/png;base64,iVBORw0KGgo=") != 1 {
		t.Fatalf("double-appended image: %s", got)
	}
}

func TestMaybeEnsureForcesToolChoiceOnImageIntent(t *testing.T) {
	auth := &cliproxyauth.Auth{
		Provider: "codex",
		Metadata: map[string]any{"codex_image_generation_bridge": true},
	}
	body := []byte(`{"model":"gpt-5.4","tools":[{"type":"function","name":"shell"}],"input":[{"type":"message","role":"user","content":[{"type":"input_text","text":"[$imagegen] 给我画一个小猫"}]}]}`)
	out := maybeEnsureCodexImageGenerationTool(body, auth, "gpt-5.4", nil)
	if got := gjson.GetBytes(out, "tools.1.type").String(); got != "image_generation" {
		t.Fatalf("tools.1.type = %q, want image_generation; body=%s", got, out)
	}
	if got := gjson.GetBytes(out, "tool_choice.type").String(); got != "image_generation" {
		t.Fatalf("tool_choice.type = %q, want image_generation; body=%s", got, out)
	}
}

func TestMaybeEnsureDoesNotForceToolChoiceWithoutIntent(t *testing.T) {
	auth := &cliproxyauth.Auth{
		Provider: "codex",
		Metadata: map[string]any{"codex_image_generation_bridge": true},
	}
	body := []byte(`{"model":"gpt-5.4","tools":[{"type":"function","name":"shell"}],"input":[{"type":"message","role":"user","content":[{"type":"input_text","text":"refactor this function"}]}]}`)
	out := maybeEnsureCodexImageGenerationTool(body, auth, "gpt-5.4", nil)
	if got := gjson.GetBytes(out, "tools.1.type").String(); got != "image_generation" {
		t.Fatalf("tools.1.type = %q, want image_generation; body=%s", got, out)
	}
	if gjson.GetBytes(out, "tool_choice.type").String() == "image_generation" {
		t.Fatalf("tool_choice should not force image_generation without intent; body=%s", out)
	}
}
