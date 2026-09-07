package antigravity

import (
	"errors"
	"testing"
	"time"
)

func TestShouldSkipProjectIDProbe(t *testing.T) {
	now := time.Date(2026, 9, 7, 12, 0, 0, 0, time.UTC)

	t.Run("empty metadata probes", func(t *testing.T) {
		if ShouldSkipProjectIDProbe(nil, now, 0) {
			t.Fatal("nil metadata should not skip")
		}
		if ShouldSkipProjectIDProbe(map[string]any{}, now, 0) {
			t.Fatal("empty metadata should not skip")
		}
	})

	t.Run("existing project id skips", func(t *testing.T) {
		meta := map[string]any{"project_id": "proj-1"}
		if !ShouldSkipProjectIDProbe(meta, now, 0) {
			t.Fatal("project_id should skip probe")
		}
	})

	t.Run("unavailable flag skips", func(t *testing.T) {
		meta := map[string]any{MetaProjectIDUnavailable: true}
		if !ShouldSkipProjectIDProbe(meta, now, 0) {
			t.Fatal("unavailable flag should skip probe")
		}
	})

	t.Run("recent probe backoff skips", func(t *testing.T) {
		meta := map[string]any{MetaProjectIDProbedAt: now.Add(-5 * time.Minute).UnixMilli()}
		if !ShouldSkipProjectIDProbe(meta, now, 30*time.Minute) {
			t.Fatal("recent probe should skip within backoff")
		}
	})

	t.Run("stale probe allows retry", func(t *testing.T) {
		meta := map[string]any{MetaProjectIDProbedAt: now.Add(-2 * time.Hour).UnixMilli()}
		if ShouldSkipProjectIDProbe(meta, now, 30*time.Minute) {
			t.Fatal("stale probe should allow retry")
		}
	})
}

func TestMarkProjectIDProbeFailureTerminal(t *testing.T) {
	now := time.Date(2026, 9, 7, 12, 0, 0, 0, time.UTC)
	meta := map[string]any{}
	MarkProjectIDProbeFailure(meta, ErrNoProjectID, now)
	if meta[MetaProjectIDUnavailable] != true {
		t.Fatalf("unavailable = %#v, want true", meta[MetaProjectIDUnavailable])
	}
	if meta[MetaProjectIDProbedAt] != now.UnixMilli() {
		t.Fatalf("probed_at = %#v, want %d", meta[MetaProjectIDProbedAt], now.UnixMilli())
	}
	if !ShouldSkipProjectIDProbe(meta, now.Add(time.Hour), 0) {
		t.Fatal("terminal failure should skip forever until cleared")
	}
}

func TestMarkProjectIDProbeFailureTransient(t *testing.T) {
	now := time.Date(2026, 9, 7, 12, 0, 0, 0, time.UTC)
	meta := map[string]any{}
	MarkProjectIDProbeFailure(meta, errors.New("http 503: busy"), now)
	if meta[MetaProjectIDUnavailable] == true {
		t.Fatal("transient error must not set unavailable")
	}
	if !ShouldSkipProjectIDProbe(meta, now.Add(time.Minute), 30*time.Minute) {
		t.Fatal("transient failure should backoff")
	}
	if ShouldSkipProjectIDProbe(meta, now.Add(31*time.Minute), 30*time.Minute) {
		t.Fatal("transient failure should retry after backoff")
	}
}

func TestClearProjectIDProbeMarkers(t *testing.T) {
	meta := map[string]any{
		MetaProjectIDUnavailable: true,
		MetaProjectIDProbedAt:    int64(1),
		MetaProjectIDProbeErr:    "x",
		"project_id":             "keep-me",
	}
	ClearProjectIDProbeMarkers(meta)
	if _, ok := meta[MetaProjectIDUnavailable]; ok {
		t.Fatal("unavailable should be cleared")
	}
	if _, ok := meta[MetaProjectIDProbedAt]; ok {
		t.Fatal("probed_at should be cleared")
	}
	if _, ok := meta[MetaProjectIDProbeErr]; ok {
		t.Fatal("probe_err should be cleared")
	}
	if meta["project_id"] != "keep-me" {
		t.Fatalf("project_id mutated: %#v", meta["project_id"])
	}
}
