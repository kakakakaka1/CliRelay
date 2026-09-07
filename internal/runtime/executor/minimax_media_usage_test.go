package executor

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/router-for-me/CLIProxyAPI/v6/internal/config"
	cliproxyexecutor "github.com/router-for-me/CLIProxyAPI/v6/sdk/cliproxy/executor"
	cliproxyusage "github.com/router-for-me/CLIProxyAPI/v6/sdk/cliproxy/usage"
	"github.com/tidwall/gjson"
)

func TestMiniMaxImageGenerationPublishesSuccessfulUsage(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/image_generation" {
			http.Error(w, "unexpected endpoint", http.StatusNotFound)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"data":{"image_base64":["AAAA"]},"base_resp":{"status_code":0}}`)
	}))
	t.Cleanup(upstream.Close)
	usagePlugin := &usageCapturePlugin{records: make(chan cliproxyusage.Record, 8)}
	cliproxyusage.RegisterPlugin(usagePlugin)
	auth := minimaxAPIKeyAuth(upstream.URL + "/v1")
	auth.ID = "minimax-success-usage-test"
	resp, err := NewMiniMaxExecutor("minimax", &config.Config{}).Execute(
		context.Background(), auth,
		cliproxyexecutor.Request{Model: "image-01", Payload: []byte(`{"model":"image-01","prompt":"a cat"}`)},
		cliproxyexecutor.Options{Alt: minimaxImageGenerationAlt},
	)
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if got := gjson.GetBytes(resp.Payload, "data.0.b64_json").String(); got != "AAAA" {
		t.Fatalf("image result = %q, want AAAA", got)
	}
	// Execute publishes synchronously into the FIFO usage queue. A sentinel
	// confirms all records it emitted were dispatched, without using a sleep
	// to guess whether an absent success record is merely delayed.
	const barrier = "minimax-success-usage-barrier"
	cliproxyusage.PublishRecord(context.Background(), cliproxyusage.Record{Provider: barrier})
	timer := time.NewTimer(2 * time.Second)
	defer timer.Stop()
	successes := 0
	for {
		select {
		case record := <-usagePlugin.records:
			if record.Provider == barrier {
				if successes != 1 {
					t.Fatalf("successful MiniMax usage records before dispatch barrier = %d, want 1", successes)
				}
				return
			}
			if record.AuthID != auth.ID || record.Provider != "minimax" {
				continue
			}
			if record.Failed || record.Model != "image-01" {
				t.Fatalf("unexpected usage outcome: failed=%v model=%q", record.Failed, record.Model)
			}
			successes++
		case <-timer.C:
			t.Fatal("usage dispatcher did not reach the test barrier")
		}
	}
}
