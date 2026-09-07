package antigravity

import (
	"errors"
	"strings"
	"time"
)

// ErrNoProjectID is returned when onboardUser completes without a project id.
var ErrNoProjectID = errors.New("no project_id in response")

// Metadata keys used to stop repeated project_id discovery storms.
// When Google returns onboard done without a project, retrying every
// token refresh / auth reload only burns CPU, writes auth files, and
// triggers watcher hot-reloads — while requests already synthesize a
// project id when metadata is empty.
const (
	MetaProjectIDUnavailable = "antigravity_project_id_unavailable"
	MetaProjectIDProbedAt    = "antigravity_project_id_probed_at"
	MetaProjectIDProbeErr    = "antigravity_project_id_probe_err"
)

// DefaultProjectIDProbeBackoff is how long transient probe failures suppress retries.
const DefaultProjectIDProbeBackoff = 30 * time.Minute

// ShouldSkipProjectIDProbe reports whether metadata says we must not hit
// loadCodeAssist/onboardUser again for project discovery.
func ShouldSkipProjectIDProbe(metadata map[string]any, now time.Time, backoff time.Duration) bool {
	if metadata == nil {
		return false
	}
	if projectIDFromMetadata(metadata) != "" {
		return true
	}
	if boolFromMetadata(metadata, MetaProjectIDUnavailable) {
		return true
	}
	if backoff <= 0 {
		backoff = DefaultProjectIDProbeBackoff
	}
	if probedAt, ok := int64FromMetadata(metadata, MetaProjectIDProbedAt); ok && probedAt > 0 {
		probed := time.UnixMilli(probedAt)
		if now.Before(probed.Add(backoff)) {
			return true
		}
	}
	return false
}

// MarkProjectIDProbeFailure records a probe failure. Terminal ErrNoProjectID
// sets the unavailable flag so healthy accounts without a Google project stop
// being re-onboarded; other errors only set a backoff timestamp.
func MarkProjectIDProbeFailure(metadata map[string]any, err error, now time.Time) {
	if metadata == nil {
		return
	}
	if now.IsZero() {
		now = time.Now()
	}
	metadata[MetaProjectIDProbedAt] = now.UnixMilli()
	if err != nil {
		msg := strings.TrimSpace(err.Error())
		if len(msg) > 200 {
			msg = msg[:200]
		}
		if msg != "" {
			metadata[MetaProjectIDProbeErr] = msg
		}
	}
	if errors.Is(err, ErrNoProjectID) || (err != nil && strings.Contains(err.Error(), "no project_id in response")) {
		metadata[MetaProjectIDUnavailable] = true
	}
}

// ClearProjectIDProbeMarkers removes backoff/unavailable markers after a successful discovery.
func ClearProjectIDProbeMarkers(metadata map[string]any) {
	if metadata == nil {
		return
	}
	delete(metadata, MetaProjectIDUnavailable)
	delete(metadata, MetaProjectIDProbedAt)
	delete(metadata, MetaProjectIDProbeErr)
}

func projectIDFromMetadata(metadata map[string]any) string {
	raw, ok := metadata["project_id"]
	if !ok || raw == nil {
		return ""
	}
	switch v := raw.(type) {
	case string:
		return strings.TrimSpace(v)
	case []byte:
		return strings.TrimSpace(string(v))
	default:
		return ""
	}
}

func boolFromMetadata(metadata map[string]any, key string) bool {
	raw, ok := metadata[key]
	if !ok || raw == nil {
		return false
	}
	switch v := raw.(type) {
	case bool:
		return v
	case string:
		switch strings.ToLower(strings.TrimSpace(v)) {
		case "1", "true", "yes", "y":
			return true
		}
	}
	return false
}

func int64FromMetadata(metadata map[string]any, key string) (int64, bool) {
	raw, ok := metadata[key]
	if !ok || raw == nil {
		return 0, false
	}
	switch v := raw.(type) {
	case int:
		return int64(v), true
	case int64:
		return v, true
	case float64:
		return int64(v), true
	case string:
		trimmed := strings.TrimSpace(v)
		if trimmed == "" {
			return 0, false
		}
		var n int64
		for _, ch := range trimmed {
			if ch < '0' || ch > '9' {
				return 0, false
			}
			n = n*10 + int64(ch-'0')
		}
		return n, true
	}
	return 0, false
}
