package executor_test

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/router-for-me/CLIProxyAPI/v6/internal/config"
	"github.com/router-for-me/CLIProxyAPI/v6/internal/registry"
	"github.com/router-for-me/CLIProxyAPI/v6/internal/runtime/executor"
	"github.com/router-for-me/CLIProxyAPI/v6/sdk/api/handlers"
	"github.com/router-for-me/CLIProxyAPI/v6/sdk/api/handlers/openai"
	coreauth "github.com/router-for-me/CLIProxyAPI/v6/sdk/cliproxy/auth"
	"github.com/router-for-me/CLIProxyAPI/v6/sdk/cliproxy/usage"
	"github.com/tidwall/gjson"
)

type miniMaxImageRecordCapture struct{ records chan usage.Record }

func (p *miniMaxImageRecordCapture) HandleUsage(_ context.Context, record usage.Record) {
	if record.AuthID == "minimax-public-images-test" || record.Provider == "minimax-images-barrier" {
		p.records <- record
	}
}

func TestMiniMaxPublicImagesBatchPublishesOneRecordPerImage(t *testing.T) {
	var calls atomic.Int32
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		body, _ := io.ReadAll(r.Body)
		if r.URL.Path != "/v1/image_generation" || gjson.GetBytes(body, "n").Int() != 1 {
			http.Error(w, "expected a single-image generation call", http.StatusBadRequest)
			return
		}
		_, _ = io.WriteString(w, `{"data":{"image_base64":["AAAA"]},"base_resp":{"status_code":0}}`)
	}))
	defer upstream.Close()
	manager := coreauth.NewManager(nil, nil, nil)
	manager.RegisterExecutor(executor.NewMiniMaxExecutor("minimax", &config.Config{}))
	auth := &coreauth.Auth{ID: "minimax-public-images-test", Provider: "minimax", Status: coreauth.StatusActive,
		Attributes: map[string]string{"api_key": "test-key", "base_url": upstream.URL + "/v1"}}
	if _, err := manager.Register(context.Background(), auth); err != nil {
		t.Fatal(err)
	}
	registry.GetGlobalRegistry().RegisterClient(auth.ID, "minimax", registry.GetMiniMaxModels())
	defer registry.GetGlobalRegistry().UnregisterClient(auth.ID)
	capture := &miniMaxImageRecordCapture{records: make(chan usage.Record, 8)}
	usage.RegisterPlugin(capture)
	handler := openai.NewOpenAIImagesAPIHandler(handlers.NewBaseAPIHandlers(&config.SDKConfig{}, manager))
	router := gin.New()
	router.POST("/v1/images/generations", handler.Generations)
	request := httptest.NewRequest(http.MethodPost, "/v1/images/generations", strings.NewReader(`{"model":"image-01","prompt":"a cat","n":3}`))
	request.Header.Set("Content-Type", "application/json")
	response := httptest.NewRecorder()
	router.ServeHTTP(response, request)
	if response.Code != http.StatusOK || len(gjson.GetBytes(response.Body.Bytes(), "data").Array()) != 3 || calls.Load() != 3 {
		t.Fatalf("status=%d calls=%d body=%s", response.Code, calls.Load(), response.Body.String())
	}
	usage.PublishRecord(context.Background(), usage.Record{Provider: "minimax-images-barrier"})
	timer := time.NewTimer(2 * time.Second)
	defer timer.Stop()
	records := 0
	for {
		select {
		case record := <-capture.records:
			if record.Provider == "minimax-images-barrier" {
				if records != 3 {
					t.Fatalf("billable single-image records=%d, want 3", records)
				}
				return
			}
			if record.Failed || record.Model != "image-01" {
				t.Fatalf("unexpected image usage record: failed=%v model=%q", record.Failed, record.Model)
			}
			records++
		case <-timer.C:
			t.Fatal("usage dispatch barrier not reached")
		}
	}
}
