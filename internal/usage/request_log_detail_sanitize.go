package usage

import (
	"encoding/json"
	"strings"
	"unicode/utf8"
)

// Max compact error payload retained for failed requests when body storage is off.
// Large enough for typical upstream JSON errors, small enough to avoid reintroducing
// full conversation/response bodies through the failure path.
const maxFailedOutputContentBytes = 8 * 1024

// stripStoredRequestDetailBodies preserves diagnostic metadata while ensuring
// request/response bodies cannot be retained indirectly in detail_content when
// full body storage is disabled.
func stripStoredRequestDetailBodies(raw string) string {
	sanitized, _ := sanitizeStoredRequestDetailBodies(raw)
	return sanitized
}

// compactFailedOutputContent keeps a short upstream error payload for the
// management UI when full body storage is disabled. Successful request/response
// bodies must not pass through this path.
func compactFailedOutputContent(raw string) string {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return ""
	}
	if len(raw) <= maxFailedOutputContentBytes {
		return raw
	}
	// Prefer keeping valid JSON when possible; otherwise hard-truncate.
	if json.Valid([]byte(raw)) {
		// Truncate with a clear marker while remaining valid-ish text for the modal.
		// Keep the leading portion which usually contains error.message/type.
		cut := maxFailedOutputContentBytes
		for cut > 0 && !utf8.ValidString(raw[:cut]) {
			cut--
		}
		if cut <= 0 {
			return raw[:maxFailedOutputContentBytes]
		}
		return raw[:cut] + "…[truncated]"
	}
	cut := maxFailedOutputContentBytes
	for cut > 0 && !utf8.ValidString(raw[:cut]) {
		cut--
	}
	if cut <= 0 {
		return raw[:maxFailedOutputContentBytes]
	}
	return raw[:cut] + "…[truncated]"
}

func sanitizeStoredRequestDetailBodies(raw string) (string, bool) {
	if strings.TrimSpace(raw) == "" {
		return raw, false
	}
	var detail map[string]any
	if err := json.Unmarshal([]byte(raw), &detail); err != nil {
		return "", true
	}
	stripExchangeField(detail, "upstream", "request_log")
	stripExchangeField(detail, "response", "upstream_log")
	data, err := json.Marshal(detail)
	if err != nil {
		return "", true
	}
	sanitized := string(data)
	return sanitized, sanitized != raw
}

func stripExchangeField(detail map[string]any, section, field string) {
	value, ok := detail[section].(map[string]any)
	if !ok {
		return
	}
	raw, ok := value[field].(string)
	if !ok || raw == "" {
		return
	}
	value[field] = stripAPIExchangeBody(raw)
}

func stripAPIExchangeBody(raw string) string {
	lines := strings.Split(raw, "\n")
	out := make([]string, 0, len(lines))
	skippingBody := false
	for _, line := range lines {
		if strings.HasPrefix(line, "=== API ") {
			skippingBody = false
		}
		if skippingBody {
			continue
		}
		out = append(out, line)
		if line == "Body:" {
			out = append(out, "<not stored>")
			skippingBody = true
		}
	}
	return strings.TrimSpace(strings.Join(out, "\n"))
}
