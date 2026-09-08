package executor

import (
	"context"
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/router-for-me/CLIProxyAPI/v6/internal/config"
	cliproxyauth "github.com/router-for-me/CLIProxyAPI/v6/sdk/cliproxy/auth"
	cliproxyexecutor "github.com/router-for-me/CLIProxyAPI/v6/sdk/cliproxy/executor"
	coreusage "github.com/router-for-me/CLIProxyAPI/v6/sdk/cliproxy/usage"
	"github.com/tidwall/gjson"
)

type alphaSearchTransport func(*http.Request) (*http.Response, error)

func (f alphaSearchTransport) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func TestAlphaSearchExecutorOAuthAndWebsocketDispatch(t *testing.T) {
	for _, transport := range []string{"http", "auto-websocket", "websocket"} {
		t.Run(transport, func(t *testing.T) {
			auth := codexOAuthAdmissionTestAuth(true, nil)
			auth.Attributes = map[string]string{"base_url": "https://untrusted.invalid", "websockets": "true"}
			ctx := contextWithCodexAdmissionHeaders(http.Header{"User-Agent": {"codex_cli_rs/0.153.4"}, "Authorization": {"Bearer downstream-secret"}, "Chatgpt-Account-Id": {"untrusted-account"}})
			ctx = cliproxyexecutor.WithDownstreamWebsocket(ctx)
			calls := 0
			const response = `{"id":"search-session","results":[{"url":"https://example.org","opaque":{"key":1}}]}`
			ctx = cliproxyexecutor.WithRoundTripper(ctx, alphaSearchTransport(func(r *http.Request) (*http.Response, error) {
				calls++
				if r.URL.String() != "https://chatgpt.com/backend-api/codex/alpha/search" {
					t.Errorf("URL=%s", r.URL)
				}
				if r.Method != http.MethodPost || r.Header.Get("Accept") != "application/json" || r.Header.Get("Authorization") != "Bearer codex-access-token" || r.Header.Get("Chatgpt-Account-Id") != "acct-test" {
					t.Errorf("incorrect method or credential headers")
				}
				body, err := io.ReadAll(r.Body)
				if err != nil {
					return nil, err
				}
				if gjson.GetBytes(body, "model").String() != "upstream-model" {
					t.Errorf("alias not rewritten: %s", body)
				}
				for _, key := range []string{"prompt_cache_key", "prompt_cache_retention", "instructions", "store", "reasoning"} {
					if gjson.GetBytes(body, key).Exists() {
						t.Errorf("unexpected %s in %s", key, body)
					}
				}
				if gjson.GetBytes(body, "commands.search_query.0.q").String() != "test" || gjson.GetBytes(body, "settings.external_web_access").Type != gjson.True || gjson.GetBytes(body, "commands.prompt_cache_key").String() != "nested-preserved" {
					t.Errorf("search payload changed: %s", body)
				}
				return &http.Response{StatusCode: 200, Header: http.Header{"Content-Type": {"application/json"}}, Body: io.NopCloser(strings.NewReader(response))}, nil
			}))
			req := cliproxyexecutor.Request{Model: "upstream-model", Payload: []byte(`{"id":"search-session","model":"alias","commands":{"search_query":[{"q":"test"}],"prompt_cache_key":"nested-preserved"},"settings":{"external_web_access":true},"prompt_cache_key":"remove","prompt_cache_retention":"24h"}`)}
			opts := cliproxyexecutor.Options{Alt: "alpha/search"}
			var resp cliproxyexecutor.Response
			var err error
			switch transport {
			case "http":
				resp, err = NewCodexExecutor(&config.Config{}).Execute(ctx, auth, req, opts)
			case "auto-websocket":
				resp, err = NewCodexAutoExecutor(&config.Config{}).Execute(ctx, auth, req, opts)
			case "websocket":
				resp, err = NewCodexWebsocketsExecutor(&config.Config{}).Execute(ctx, auth, req, opts)
			}
			if err != nil || string(resp.Payload) != response || calls != 1 {
				t.Fatalf("calls=%d body=%s err=%v", calls, resp.Payload, err)
			}
		})
	}
}

func TestAlphaSearchExecutorFailuresAndUsage(t *testing.T) {
	auth := &cliproxyauth.Auth{ID: "search-key", Provider: "codex", Attributes: map[string]string{"api_key": "upstream-key", "alpha_search": "true", "base_url": "https://search.example/v1/"}}
	const model = "alpha-search-usage-test"
	req := cliproxyexecutor.Request{Model: model, Payload: []byte(`{"model":"alpha-search-usage-test","commands":{"search_query":[{"q":"test"}]}}`)}
	opts := cliproxyexecutor.Options{Alt: "alpha/search"}
	for _, status := range []int{200, 401, 429} {
		t.Run(http.StatusText(status), func(t *testing.T) {
			plugin := &tenantUsageCapturePlugin{records: make(chan coreusage.Record, 16)}
			coreusage.RegisterPlugin(plugin)
			ctx := cliproxyexecutor.WithRoundTripper(context.Background(), alphaSearchTransport(func(r *http.Request) (*http.Response, error) {
				if r.URL.String() != "https://search.example/v1/alpha/search" || r.Header.Get("Authorization") != "Bearer upstream-key" {
					t.Errorf("incorrect target/auth")
				}
				return &http.Response{StatusCode: status, Header: http.Header{"Retry-After": {"7"}}, Body: io.NopCloser(strings.NewReader(`{"id":"search-result"}`))}, nil
			}))
			resp, err := NewCodexExecutor(&config.Config{}).Execute(ctx, auth, req, opts)
			if status == 200 {
				if err != nil || string(resp.Payload) != `{"id":"search-result"}` {
					t.Fatalf("resp=%s err=%v", resp.Payload, err)
				}
			} else {
				var coded interface{ StatusCode() int }
				if !errors.As(err, &coded) || coded.StatusCode() != status {
					t.Fatalf("status=%d err=%v", status, err)
				}
			}
			timeout := time.After(2 * time.Second)
			for {
				select {
				case record := <-plugin.records:
					if record.Model != model {
						continue
					}
					if record.Failed != (status != 200) || record.Detail.TotalTokens != 0 {
						t.Fatalf("incorrect usage: %+v", record)
					}
					return
				case <-timeout:
					t.Fatal("no usage record")
				}
			}
		})
	}
	t.Run("cancel", func(t *testing.T) {
		started := make(chan struct{})
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		ctx = cliproxyexecutor.WithRoundTripper(ctx, alphaSearchTransport(func(r *http.Request) (*http.Response, error) {
			close(started)
			<-r.Context().Done()
			return nil, r.Context().Err()
		}))
		result := make(chan error, 1)
		go func() { _, err := NewCodexExecutor(&config.Config{}).Execute(ctx, auth, req, opts); result <- err }()
		select {
		case <-started:
			cancel()
		case <-time.After(2 * time.Second):
			t.Fatal("transport not reached")
		}
		select {
		case err := <-result:
			if !errors.Is(err, context.Canceled) {
				t.Fatalf("err=%v", err)
			}
		case <-time.After(2 * time.Second):
			t.Fatal("cancellation not propagated")
		}
	})
	t.Run("admission", func(t *testing.T) {
		oauth := codexOAuthAdmissionTestAuth(true, nil)
		ctx := contextWithCodexAdmissionHeaders(http.Header{"User-Agent": {"curl/8"}})
		ctx = cliproxyexecutor.WithRoundTripper(ctx, alphaSearchTransport(func(*http.Request) (*http.Response, error) {
			t.Error("denied request reached upstream")
			return nil, errors.New("unexpected network")
		}))
		_, err := NewCodexExecutor(&config.Config{}).Execute(ctx, oauth, req, opts)
		var coded interface{ StatusCode() int }
		if !errors.As(err, &coded) || coded.StatusCode() != 403 {
			t.Fatalf("err=%v", err)
		}
	})
}
