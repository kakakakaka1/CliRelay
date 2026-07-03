package auth

import (
	"bytes"
	"encoding/json"
	"net/http"
	"strings"

	sdktranslator "github.com/router-for-me/CLIProxyAPI/v6/sdk/translator"
)

const responsesStreamCompletedType = "response.completed"

type responsesStreamCompletionTracker struct {
	buffer    []byte
	completed bool
}

func newResponsesStreamCompletionTracker(sourceFormat sdktranslator.Format) *responsesStreamCompletionTracker {
	if sourceFormat != sdktranslator.FormatOpenAIResponse {
		return nil
	}
	return &responsesStreamCompletionTracker{}
}

func (t *responsesStreamCompletionTracker) Observe(chunk []byte) {
	if t == nil || t.completed || len(chunk) == 0 {
		return
	}
	t.buffer = append(t.buffer, chunk...)
	for {
		block, rest, ok := takeSSEBlock(t.buffer)
		if !ok {
			return
		}
		t.buffer = rest
		if responsesSSEBlockCompleted(block) {
			t.completed = true
			return
		}
	}
}

func (t *responsesStreamCompletionTracker) ErrIfIncomplete() error {
	if t == nil || t.completed {
		return nil
	}
	if len(bytes.TrimSpace(t.buffer)) > 0 && responsesSSEBlockCompleted(t.buffer) {
		return nil
	}
	return &Error{
		Code:       "response_stream_incomplete",
		Message:    "upstream responses stream closed before response.completed",
		Retryable:  true,
		HTTPStatus: http.StatusBadGateway,
	}
}

func takeSSEBlock(buffer []byte) ([]byte, []byte, bool) {
	end, delimLen := firstSSEBlockEnd(buffer)
	if end < 0 {
		return nil, buffer, false
	}
	block := buffer[:end]
	rest := buffer[end+delimLen:]
	return block, rest, true
}

func firstSSEBlockEnd(buffer []byte) (int, int) {
	type delimiter struct {
		value []byte
		len   int
	}
	delimiters := []delimiter{
		{value: []byte("\n\n"), len: 2},
		{value: []byte("\r\n\r\n"), len: 4},
		{value: []byte("\r\r"), len: 2},
	}
	best := -1
	bestLen := 0
	for _, delimiter := range delimiters {
		idx := bytes.Index(buffer, delimiter.value)
		if idx < 0 {
			continue
		}
		if best < 0 || idx < best {
			best = idx
			bestLen = delimiter.len
		}
	}
	return best, bestLen
}

func responsesSSEBlockCompleted(block []byte) bool {
	var eventType string
	var dataLines []string
	for _, rawLine := range bytes.Split(block, []byte("\n")) {
		line := strings.TrimSpace(strings.TrimSuffix(string(rawLine), "\r"))
		if line == "" || strings.HasPrefix(line, ":") {
			continue
		}
		if value, ok := strings.CutPrefix(line, "event:"); ok {
			eventType = strings.TrimSpace(value)
			continue
		}
		if value, ok := strings.CutPrefix(line, "data:"); ok {
			dataLines = append(dataLines, strings.TrimSpace(value))
		}
	}
	if eventType == responsesStreamCompletedType {
		return true
	}
	if len(dataLines) == 0 {
		return false
	}
	data := strings.TrimSpace(strings.Join(dataLines, "\n"))
	if data == "" || data == "[DONE]" {
		return false
	}
	var payload struct {
		Type string `json:"type"`
	}
	if err := json.Unmarshal([]byte(data), &payload); err != nil {
		return false
	}
	return strings.TrimSpace(payload.Type) == responsesStreamCompletedType
}
