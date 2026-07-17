package middleware

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/gin-gonic/gin"
)

func TestQuotaMiddlewareEnforcesConcurrencyLimitPerKey(t *testing.T) {
	gin.SetMode(gin.TestMode)
	resetQuotaMiddlewareState(t)

	entered := make(chan struct{})
	release := make(chan struct{})
	var enteredOnce sync.Once

	router := gin.New()
	router.Use(func(c *gin.Context) {
		key := c.GetHeader("X-Test-Key")
		if key == "" {
			key = "key-a"
		}
		c.Set("apiKey", key)
		c.Set("accessMetadata", map[string]string{"concurrency-limit": "1"})
		c.Next()
	})
	router.Use(QuotaMiddleware())
	router.POST("/v1/chat/completions", func(c *gin.Context) {
		if key, _ := c.Get("apiKey"); key == "key-a" {
			enteredOnce.Do(func() { close(entered) })
			<-release
		}
		c.Status(http.StatusNoContent)
	})

	firstDone := make(chan struct{})
	first := httptest.NewRecorder()
	go func() {
		defer close(firstDone)
		router.ServeHTTP(first, newQuotaPostRequest("key-a"))
	}()

	<-entered

	secondSameKey := httptest.NewRecorder()
	router.ServeHTTP(secondSameKey, newQuotaPostRequest("key-a"))
	if secondSameKey.Code != http.StatusTooManyRequests {
		t.Fatalf("same-key concurrent status = %d, want %d", secondSameKey.Code, http.StatusTooManyRequests)
	}
	if !strings.Contains(secondSameKey.Body.String(), "Concurrent request limit exceeded") {
		t.Fatalf("concurrency body = %s, want clear concurrency wording", secondSameKey.Body.String())
	}
	if got := secondSameKey.Header().Get("X-CliRelay-Quota-Code"); got != "concurrency_limit_exceeded" {
		t.Fatalf("concurrency header code = %q", got)
	}

	secondOtherKey := httptest.NewRecorder()
	router.ServeHTTP(secondOtherKey, newQuotaPostRequest("key-b"))
	if secondOtherKey.Code != http.StatusNoContent {
		t.Fatalf("other-key concurrent status = %d, want %d", secondOtherKey.Code, http.StatusNoContent)
	}

	close(release)
	<-firstDone
	if first.Code != http.StatusNoContent {
		t.Fatalf("first status = %d, want %d", first.Code, http.StatusNoContent)
	}

	afterRelease := httptest.NewRecorder()
	router.ServeHTTP(afterRelease, newQuotaPostRequest("key-a"))
	if afterRelease.Code != http.StatusNoContent {
		t.Fatalf("after-release status = %d, want %d", afterRelease.Code, http.StatusNoContent)
	}
}

func TestQuotaMiddlewareDailySpendingLimit(t *testing.T) {
	gin.SetMode(gin.TestMode)
	resetQuotaMiddlewareState(t)

	queryTodayCostByKeyFunc = func(key string) (float64, error) {
		if key == "key-over" {
			return 50, nil
		}
		return 20, nil
	}

	router := gin.New()
	router.Use(func(c *gin.Context) {
		c.Set("apiKey", c.GetHeader("X-Test-Key"))
		c.Set("accessMetadata", map[string]string{"daily-spending-limit": "50"})
		c.Next()
	})
	router.Use(QuotaMiddleware())
	router.POST("/v1/chat/completions", func(c *gin.Context) {
		c.Status(http.StatusNoContent)
	})

	allowed := httptest.NewRecorder()
	router.ServeHTTP(allowed, newQuotaPostRequest("key-under"))
	if allowed.Code != http.StatusNoContent {
		t.Fatalf("under-limit status = %d, want %d", allowed.Code, http.StatusNoContent)
	}

	blocked := httptest.NewRecorder()
	router.ServeHTTP(blocked, newQuotaPostRequest("key-over"))
	if blocked.Code != http.StatusTooManyRequests {
		t.Fatalf("over-limit status = %d, want %d", blocked.Code, http.StatusTooManyRequests)
	}

	var body struct {
		Error struct {
			Code    string `json:"code"`
			Message string `json:"message"`
		} `json:"error"`
	}
	if err := json.Unmarshal(blocked.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode error body: %v", err)
	}
	if body.Error.Code != "daily_spending_limit_exceeded" {
		t.Fatalf("error code = %q, want daily_spending_limit_exceeded", body.Error.Code)
	}
	if !strings.Contains(body.Error.Message, "Daily spending limit exceeded") {
		t.Fatalf("message = %q, want daily spending wording", body.Error.Message)
	}
	if got := blocked.Header().Get("X-CliRelay-Quota-Code"); got != "daily_spending_limit_exceeded" {
		t.Fatalf("X-CliRelay-Quota-Code = %q", got)
	}
	if got := blocked.Header().Get("X-CliRelay-Quota-Rejected-By"); got != "daily_spending" {
		t.Fatalf("X-CliRelay-Quota-Rejected-By = %q", got)
	}
}

func TestQuotaMiddlewareDistinctLimitMessages(t *testing.T) {
	gin.SetMode(gin.TestMode)
	resetQuotaMiddlewareState(t)

	countTodayByKeyFunc = func(string) (int64, error) { return 10, nil }
	countTotalByKeyFunc = func(string) (int64, error) { return 100, nil }
	queryTotalCostByKeyFunc = func(string) (float64, error) { return 25, nil }

	cases := []struct {
		name       string
		metadata   map[string]string
		wantCode   string
		wantPhrase string
	}{
		{
			name:       "daily requests",
			metadata:   map[string]string{"daily-limit": "10"},
			wantCode:   "daily_limit_exceeded",
			wantPhrase: "Daily request limit exceeded",
		},
		{
			name:       "total quota",
			metadata:   map[string]string{"total-quota": "100"},
			wantCode:   "total_quota_exceeded",
			wantPhrase: "Total request quota exhausted",
		},
		{
			name:       "rpm",
			metadata:   map[string]string{"rpm-limit": "1"},
			wantCode:   "rpm_limit_exceeded",
			wantPhrase: "Requests-per-minute (RPM) limit exceeded",
		},
		{
			name:       "spending",
			metadata:   map[string]string{"spending-limit": "25"},
			wantCode:   "spending_limit_exceeded",
			wantPhrase: "Lifetime spending limit exceeded",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			resetQuotaMiddlewareState(t)
			countTodayByKeyFunc = func(string) (int64, error) { return 10, nil }
			countTotalByKeyFunc = func(string) (int64, error) { return 100, nil }
			queryTotalCostByKeyFunc = func(string) (float64, error) { return 25, nil }

			router := gin.New()
			router.Use(func(c *gin.Context) {
				c.Set("apiKey", "key-a")
				c.Set("accessMetadata", tc.metadata)
				c.Next()
			})
			router.Use(QuotaMiddleware())
			router.POST("/v1/chat/completions", func(c *gin.Context) {
				c.Status(http.StatusNoContent)
			})

			// RPM needs two posts so the second exceeds limit 1.
			if tc.wantCode == "rpm_limit_exceeded" {
				first := httptest.NewRecorder()
				router.ServeHTTP(first, newQuotaPostRequest("key-a"))
			}

			blocked := httptest.NewRecorder()
			router.ServeHTTP(blocked, newQuotaPostRequest("key-a"))
			if blocked.Code != http.StatusTooManyRequests {
				t.Fatalf("status = %d, want 429; body=%s", blocked.Code, blocked.Body.String())
			}
			var body struct {
				Error struct {
					Code    string `json:"code"`
					Message string `json:"message"`
				} `json:"error"`
			}
			if err := json.Unmarshal(blocked.Body.Bytes(), &body); err != nil {
				t.Fatalf("decode: %v body=%s", err, blocked.Body.String())
			}
			if body.Error.Code != tc.wantCode {
				t.Fatalf("code = %q, want %q", body.Error.Code, tc.wantCode)
			}
			if !strings.Contains(body.Error.Message, tc.wantPhrase) {
				t.Fatalf("message = %q, want phrase %q", body.Error.Message, tc.wantPhrase)
			}
			if got := blocked.Header().Get("X-CliRelay-Quota-Code"); got != tc.wantCode {
				t.Fatalf("header code = %q, want %q", got, tc.wantCode)
			}
		})
	}
}

func newQuotaPostRequest(key string) *http.Request {
	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", nil)
	req.Header.Set("X-Test-Key", key)
	return req
}

func resetQuotaMiddlewareState(t *testing.T) {
	t.Helper()

	rpmTrackers = sync.Map{}
	tpmTrackers = sync.Map{}
	snapshotLimits = sync.Map{}
	inFlightMu.Lock()
	inFlightByKey = map[string]int{}
	inFlightMu.Unlock()
	countTodayByKeyFunc = func(string) (int64, error) { return 0, nil }
	countTotalByKeyFunc = func(string) (int64, error) { return 0, nil }
	queryTotalCostByKeyFunc = func(string) (float64, error) { return 0, nil }
	queryTodayCostByKeyFunc = func(string) (float64, error) { return 0, nil }
	t.Cleanup(func() {
		countTodayByKeyFunc = func(string) (int64, error) { return 0, nil }
		countTotalByKeyFunc = func(string) (int64, error) { return 0, nil }
		queryTotalCostByKeyFunc = func(string) (float64, error) { return 0, nil }
		queryTodayCostByKeyFunc = func(string) (float64, error) { return 0, nil }
	})
}
