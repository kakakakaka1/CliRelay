package executor

import (
	"context"
	"io"
	"net/http"
	"strings"
	"testing"

	xaiauth "github.com/router-for-me/CLIProxyAPI/v6/internal/auth/xai"
	"github.com/router-for-me/CLIProxyAPI/v6/internal/config"
	"github.com/router-for-me/CLIProxyAPI/v6/internal/util"
	cliproxyauth "github.com/router-for-me/CLIProxyAPI/v6/sdk/cliproxy/auth"
	cliproxyexecutor "github.com/router-for-me/CLIProxyAPI/v6/sdk/cliproxy/executor"
	sdktranslator "github.com/router-for-me/CLIProxyAPI/v6/sdk/translator"
	"github.com/tidwall/gjson"
)

type xaiStreamRoundTripperFunc func(*http.Request) (*http.Response, error)

func (f xaiStreamRoundTripperFunc) RoundTrip(req *http.Request) (*http.Response, error) {
	return f(req)
}

func TestXAIExecutorClaudeStreamMaintainsAnthropicContentBlockLifecycle(t *testing.T) {
	claudeRequest := []byte(`{
		"model":"grok-composer-2.5-fast",
		"stream":true,
		"messages":[{"role":"user","content":"Inspect the repo"}],
		"tools":[{"name":"Read","description":"Read a file","input_schema":{"type":"object","properties":{"path":{"type":"string"}}}}]
	}`)
	upstreamEvents := []string{
		`data: {"type":"response.created","response":{"id":"resp_1","model":"grok-composer-2.5-fast"}}`,
		`data: {"type":"response.output_item.added","item":{"type":"message","status":"in_progress"},"output_index":1}`,
		`data: {"type":"response.content_part.added","part":{"type":"output_text"},"content_index":0,"output_index":1}`,
		`data: {"type":"response.output_text.delta","delta":"inspect repo","output_index":1}`,
		`data: {"type":"response.output_item.added","item":{"type":"function_call","id":"fc_a","call_id":"call_a","name":"Read","status":"in_progress"},"output_index":2}`,
		`data: {"type":"response.function_call_arguments.delta","item_id":"fc_a","delta":"{\"path\":\"README.md\"}","output_index":2}`,
		`data: {"type":"response.output_item.done","item":{"type":"function_call","id":"fc_a","call_id":"call_a","name":"Read","arguments":"{\"path\":\"README.md\"}"},"output_index":2}`,
		`data: {"type":"response.content_part.done","part":{"type":"output_text"},"content_index":0,"output_index":1}`,
		`data: {"type":"response.completed","response":{"usage":{"input_tokens":11,"output_tokens":7}}}`,
	}

	ctx := context.WithValue(context.Background(), util.ContextKeyRoundTripper, xaiStreamRoundTripperFunc(func(req *http.Request) (*http.Response, error) {
		wantURL := strings.TrimRight(xaiauth.CLIChatProxyBaseURL, "/") + "/responses"
		if got := req.URL.String(); got != wantURL {
			t.Fatalf("upstream URL = %q, want %q", got, wantURL)
		}
		body, err := io.ReadAll(req.Body)
		if err != nil {
			t.Fatalf("read translated request: %v", err)
		}
		if got := gjson.GetBytes(body, "tools.0.name").String(); got != "Read" {
			t.Fatalf("translated tool name = %q, want Read: %s", got, body)
		}
		if got := gjson.GetBytes(body, "tool_choice").String(); got != "auto" {
			t.Fatalf("translated tool_choice = %q, want auto: %s", got, body)
		}
		return &http.Response{
			StatusCode: http.StatusOK,
			Status:     "200 OK",
			Header:     http.Header{"Content-Type": []string{"text/event-stream"}},
			Body:       io.NopCloser(strings.NewReader(strings.Join(upstreamEvents, "\n\n") + "\n\n")),
			Request:    req,
		}, nil
	}))

	auth := &cliproxyauth.Auth{
		Provider: "xai",
		Attributes: map[string]string{
			"auth_kind": "oauth",
			"api_key":   "oauth-token",
			"base_url":  xaiauth.DefaultAPIBaseURL,
			"using_api": "false",
		},
	}
	result, err := NewXAIExecutor(&config.Config{}).ExecuteStream(ctx, auth, cliproxyexecutor.Request{
		Model:   "grok-composer-2.5-fast",
		Payload: claudeRequest,
	}, cliproxyexecutor.Options{
		Stream:          true,
		OriginalRequest: claudeRequest,
		SourceFormat:    sdktranslator.FormatClaude,
	})
	if err != nil {
		t.Fatalf("ExecuteStream returned error: %v", err)
	}

	var output strings.Builder
	for chunk := range result.Chunks {
		if chunk.Err != nil {
			t.Fatalf("stream chunk error: %v", chunk.Err)
		}
		output.Write(chunk.Payload)
	}
	assertXAIClaudeBlockLifecycle(t, output.String())
}

func TestXAIPrepareResponsesRequestEmptyToolsOmitsToolChoice(t *testing.T) {
	payload := []byte(`{
		"model":"grok-4",
		"messages":[{"role":"user","content":"Create a title"}],
		"tools":[],
		"tool_choice":{"type":"auto"}
	}`)

	_, body, _, err := NewXAIExecutor(&config.Config{}).prepareResponsesRequest(
		context.Background(),
		&cliproxyauth.Auth{Provider: "xai"},
		cliproxyexecutor.Request{Model: "grok-4", Payload: payload},
		cliproxyexecutor.Options{OriginalRequest: payload, SourceFormat: sdktranslator.FormatClaude},
		false,
	)
	if err != nil {
		t.Fatalf("prepareResponsesRequest returned error: %v", err)
	}
	if gjson.GetBytes(body, "tool_choice").Exists() {
		t.Fatalf("empty tools must not reach xAI with tool_choice: %s", body)
	}
	if tools := gjson.GetBytes(body, "tools"); tools.Exists() && len(tools.Array()) != 0 {
		t.Fatalf("unexpected tools in xAI request: %s", body)
	}
}

func assertXAIClaudeBlockLifecycle(t *testing.T, output string) {
	t.Helper()
	open := map[int]bool{}
	started := map[int]bool{}
	for _, line := range strings.Split(output, "\n") {
		if !strings.HasPrefix(line, "data: ") {
			continue
		}
		payload := gjson.Parse(strings.TrimPrefix(line, "data: "))
		index := int(payload.Get("index").Int())
		switch payload.Get("type").String() {
		case "content_block_start":
			if started[index] {
				t.Fatalf("content block index %d was started more than once: %s", index, output)
			}
			started[index] = true
			open[index] = true
		case "content_block_delta":
			if !open[index] {
				t.Fatalf("content block delta for unopened index %d: %s", index, output)
			}
		case "content_block_stop":
			if !open[index] {
				t.Fatalf("content block stop for unopened index %d: %s", index, output)
			}
			delete(open, index)
		case "message_stop":
			if len(open) != 0 {
				t.Fatalf("message_stop with open content blocks %v: %s", open, output)
			}
		}
	}
}
