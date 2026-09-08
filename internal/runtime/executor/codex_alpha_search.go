package executor

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"strings"

	cliproxyauth "github.com/router-for-me/CLIProxyAPI/v6/sdk/cliproxy/auth"
	cliproxyexecutor "github.com/router-for-me/CLIProxyAPI/v6/sdk/cliproxy/executor"
	log "github.com/sirupsen/logrus"
)

// Alpha Search has its own wire schema. Translation, payload overrides and the
// Responses cache helper would add fields the search upstream rejects.
func (e *CodexExecutor) executeAlphaSearch(ctx context.Context, auth *cliproxyauth.Auth, req cliproxyexecutor.Request, opts cliproxyexecutor.Options) (resp cliproxyexecutor.Response, err error) {
	execCtx := newExecutionContext(ctx, e.Identifier(), e.cfg, auth, req, opts, ExecutionOptions{})
	reporter := execCtx.Reporter()
	defer reporter.trackFailure(execCtx.Context, &err)
	if opts.Stream {
		return resp, statusErr{code: http.StatusBadRequest, msg: "Alpha Search does not support streaming"}
	}
	if !cliproxyauth.SupportsCodexAlphaSearch(auth) {
		return resp, statusErr{code: http.StatusBadRequest, msg: "credential does not support Codex Alpha Search"}
	}
	if err = enforceCodexClientAdmission(execCtx.Context, e.cfg, auth); err != nil {
		return resp, err
	}
	var payload map[string]json.RawMessage
	if json.Unmarshal(req.Payload, &payload) != nil || payload == nil || strings.TrimSpace(execCtx.BaseModel) == "" {
		return resp, statusErr{code: http.StatusBadRequest, msg: "Alpha Search requires a JSON object and model"}
	}
	if string(bytes.TrimSpace(payload["stream"])) == "true" {
		return resp, statusErr{code: http.StatusBadRequest, msg: "Alpha Search does not support streaming"}
	}
	// Manager has resolved any permitted OAuth/API-key alias before execution.
	payload["model"], err = json.Marshal(execCtx.BaseModel)
	if err != nil {
		return resp, err
	}
	delete(payload, "prompt_cache_key")
	delete(payload, "prompt_cache_retention")
	body, err := json.Marshal(payload)
	if err != nil {
		return resp, err
	}
	token, baseURL := codexCreds(auth)
	if strings.TrimSpace(auth.Attributes["api_key"]) == "" {
		// An OAuth token must never be sent to a caller/config supplied relay URL.
		baseURL = "https://chatgpt.com/backend-api/codex"
	}
	url := strings.TrimRight(strings.TrimSpace(baseURL), "/") + "/alpha/search"
	httpReq, err := http.NewRequestWithContext(execCtx.Context, http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return resp, err
	}
	applyCodexHeaders(httpReq, e.cfg, auth, token, false)
	recorder := execCtx.Recorder()
	recorder.RecordRequest(url, http.MethodPost, httpReq.Header.Clone(), body)
	//nolint:bodyclose // The response body is closed by the defer below.
	httpResp, err := execCtx.HTTPClient(0).Do(httpReq)
	if err != nil {
		recorder.RecordResponseError(err)
		reporter.publishFailureWithContentBytes(execCtx.Context, req.Payload, err.Error())
		return resp, err
	}
	defer func() {
		if closeErr := httpResp.Body.Close(); closeErr != nil {
			log.Errorf("codex alpha search: close response body: %v", closeErr)
		}
	}()
	recorder.RecordResponseMetadata(httpResp.StatusCode, httpResp.Header.Clone())
	if httpResp.StatusCode < 200 || httpResp.StatusCode >= 300 {
		data := readUpstreamErrorBody(e.Identifier(), httpResp.Body)
		recorder.AppendResponseChunk(data)
		reporter.publishFailureWithContentBytes(execCtx.Context, req.Payload, string(data))
		return resp, newCodexStatusErr(httpResp.StatusCode, data, httpResp.Header)
	}
	data, err := readUpstreamResponseBody(e.Identifier(), httpResp.Body)
	if err != nil {
		recorder.RecordResponseError(err)
		return resp, err
	}
	recorder.AppendResponseChunk(data)
	// Publish successful requests even when this protocol returns no token usage.
	reporter.publishWithContentBytes(execCtx.Context, parseOpenAIUsage(data), req.Payload, string(data))
	reporter.ensurePublished(execCtx.Context)
	return cliproxyexecutor.Response{Payload: data, Headers: httpResp.Header.Clone()}, nil
}
