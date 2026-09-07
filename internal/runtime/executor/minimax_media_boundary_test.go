package executor

import (
	"context"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"

	"github.com/router-for-me/CLIProxyAPI/v6/internal/config"
	cliproxyexecutor "github.com/router-for-me/CLIProxyAPI/v6/sdk/cliproxy/executor"
)

func TestMiniMaxRejectsUnsupportedImageOperationsBeforeUpstream(t *testing.T) {
	for _, tc := range []struct {
		name, alt, model, payload string
	}{
		{"edits", "images/edits", "image-01", `{"model":"image-01","prompt":"edit","image":"data:image/png;base64,AAAA"}`},
		{"live generation", minimaxImageGenerationAlt, "image-01-live", `{"model":"image-01-live","prompt":"draw"}`},
		{"unknown revision", minimaxImageGenerationAlt, "image-01-preview", `{"model":"image-01-preview","prompt":"draw"}`},
		{"batch executor call", minimaxImageGenerationAlt, "image-01", `{"model":"image-01","prompt":"draw","n":2}`},
		{"fractional count", minimaxImageGenerationAlt, "image-01", `{"model":"image-01","prompt":"draw","n":1.5}`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var calls atomic.Int32
			upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				calls.Add(1)
				http.Error(w, "unexpected upstream call", http.StatusBadGateway)
			}))
			defer upstream.Close()
			exec := NewMiniMaxExecutor("minimax", &config.Config{})
			auth := minimaxAPIKeyAuth(upstream.URL + "/v1")
			req := cliproxyexecutor.Request{Model: tc.model, Payload: []byte(tc.payload)}
			opts := cliproxyexecutor.Options{Alt: tc.alt}
			_, err := exec.Execute(context.Background(), auth, req, opts)
			if status, ok := err.(cliproxyexecutor.StatusError); !ok || status.StatusCode() != http.StatusBadRequest {
				t.Errorf("Execute error = %v, want HTTP 400", err)
			}
			_, err = exec.ExecuteStream(context.Background(), auth, req, opts)
			if status, ok := err.(cliproxyexecutor.StatusError); !ok || status.StatusCode() != http.StatusBadRequest {
				t.Errorf("ExecuteStream error = %v, want HTTP 400", err)
			}
			if calls.Load() != 0 {
				t.Errorf("unsupported operation reached upstream %d times", calls.Load())
			}
		})
	}
}
