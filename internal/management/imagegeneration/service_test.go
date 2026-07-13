package imagegeneration

import (
	"context"
	"errors"
	"testing"
	"time"
)

func TestServiceStartAndGetLifecycle(t *testing.T) {
	t.Parallel()

	done := make(chan struct{})
	svc := NewService(func(ctx context.Context, tenantID string, payload []byte, alt string) ([]byte, error) {
		close(done)
		return []byte(`{"data":[{"b64_json":"abc"}]}`), nil
	}, "test")

	snapshot := svc.Start("tenant-a", []byte(`{"prompt":"hello"}`), "images/generations")
	if snapshot.ID == "" {
		t.Fatalf("Start() returned empty task id")
	}

	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("task did not finish in time")
	}

	got, ok := svc.Get("tenant-a", snapshot.ID)
	if !ok {
		t.Fatalf("Get(%q) returned not found", snapshot.ID)
	}
	if got.Status != "succeeded" {
		t.Fatalf("task status = %q, want succeeded", got.Status)
	}
	if got.Result == nil {
		t.Fatalf("task result is nil")
	}
}

type fakeStatusError struct {
	code int
	err  error
}

func (e fakeStatusError) Error() string {
	return e.err.Error()
}

func (e fakeStatusError) StatusCode() int {
	return e.code
}

func TestServiceCapturesStatusError(t *testing.T) {
	t.Parallel()

	done := make(chan struct{})
	svc := NewService(func(ctx context.Context, tenantID string, payload []byte, alt string) ([]byte, error) {
		close(done)
		return nil, fakeStatusError{code: 429, err: errors.New("rate limited")}
	}, "test")

	snapshot := svc.Start("tenant-a", []byte(`{"prompt":"hello"}`), "images/generations")
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("task did not finish in time")
	}

	got, ok := svc.Get("tenant-a", snapshot.ID)
	if !ok {
		t.Fatalf("Get(%q) returned not found", snapshot.ID)
	}
	if got.Status != "failed" {
		t.Fatalf("task status = %q, want failed", got.Status)
	}
	if got.Error == nil {
		t.Fatalf("task error is nil")
	}
	if status, _ := got.Error["status"].(int); status != 429 {
		t.Fatalf("task status code = %v, want 429", got.Error["status"])
	}
}

func TestServiceTaskIsTenantScoped(t *testing.T) {
	t.Parallel()

	done := make(chan struct{})
	svc := NewService(func(_ context.Context, tenantID string, _ []byte, _ string) ([]byte, error) {
		if tenantID != "tenant-a" {
			t.Errorf("tenantID = %q", tenantID)
		}
		close(done)
		return []byte(`{"data":[]}`), nil
	}, "test")

	task := svc.Start("tenant-a", []byte(`{"prompt":"hello"}`), "images/generations")
	<-done
	if _, ok := svc.Get("tenant-b", task.ID); ok {
		t.Fatal("tenant B can read tenant A task")
	}
	if _, ok := svc.Get("tenant-a", task.ID); !ok {
		t.Fatal("tenant A task missing")
	}
}
