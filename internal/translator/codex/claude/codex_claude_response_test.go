package claude

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"github.com/tidwall/gjson"
)

func TestConvertCodexResponseToClaude_GrokInterleavedTextAndToolCalls(t *testing.T) {
	originalRequest := []byte(`{"tools":[{"name":"Read"}]}`)
	chunks := []string{
		`data: {"type":"response.created","response":{"id":"resp_1","model":"grok-composer-2.5-fast"}}`,
		`data: {"type":"response.output_item.added","item":{"type":"message","status":"in_progress"},"output_index":1}`,
		`data: {"type":"response.content_part.added","part":{"type":"output_text"},"content_index":0,"output_index":1}`,
		`data: {"type":"response.output_text.delta","delta":"inspect repo","output_index":1}`,
		`data: {"type":"response.output_item.added","item":{"type":"function_call","id":"fc_a","call_id":"call_a","name":"Read","status":"in_progress"},"output_index":2}`,
		`data: {"type":"response.function_call_arguments.delta","item_id":"fc_a","delta":"{\"path\":\"README.md\"}","output_index":2}`,
		`data: {"type":"response.function_call_arguments.done","item_id":"fc_a","arguments":"{\"path\":\"README.md\"}","output_index":2}`,
		`data: {"type":"response.output_item.done","item":{"type":"function_call","id":"fc_a","call_id":"call_a","name":"Read","arguments":"{\"path\":\"README.md\"}"},"output_index":2}`,
		`data: {"type":"response.output_item.added","item":{"type":"function_call","id":"fc_b","call_id":"call_b","name":"Read","status":"in_progress"},"output_index":3}`,
		`data: {"type":"response.function_call_arguments.done","item_id":"fc_b","arguments":"{\"path\":\"main.go\"}","output_index":3}`,
		`data: {"type":"response.content_part.done","part":{"type":"output_text"},"content_index":0,"output_index":1}`,
		`data: {"type":"response.output_item.done","item":{"type":"message","status":"completed"},"output_index":1}`,
		`data: {"type":"response.output_item.done","item":{"type":"function_call","id":"fc_b","call_id":"call_b","name":"Read","arguments":"{\"path\":\"main.go\"}"},"output_index":3}`,
		`data: {"type":"response.completed","response":{"usage":{"input_tokens":11,"output_tokens":7}}}`,
	}

	outputs := convertCodexClaudeStream(t, originalRequest, chunks)
	assertClaudeContentBlockLifecycle(t, outputs)

	joined := strings.Join(outputs, "")
	if got := strings.Count(joined, `"type":"tool_use"`); got != 2 {
		t.Fatalf("tool_use count = %d, want 2\n%s", got, joined)
	}
	for _, want := range []string{`"index":0,"content_block":{"type":"text"`, `"index":1,"content_block":{"type":"tool_use"`, `"index":2,"content_block":{"type":"tool_use"`, `README.md`, `main.go`} {
		if !strings.Contains(joined, want) {
			t.Fatalf("stream output missing %q\n%s", want, joined)
		}
	}
}

func TestConvertCodexResponseToClaude_MalformedAndOutOfOrderEventsNeverOrphanBlocks(t *testing.T) {
	tests := []struct {
		name   string
		chunks []string
		check  func(*testing.T, string)
	}{
		{
			name: "reasoning delta and stop without start",
			chunks: []string{
				`data: {"type":"response.reasoning_summary_text.delta","delta":"late"}`,
				`data: {"type":"response.reasoning_summary_part.done"}`,
			},
		},
		{
			name: "text delta without added is started lazily",
			chunks: []string{
				`data: {"type":"response.output_text.delta","delta":"hello"}`,
				`data: {"type":"response.content_part.done","part":{"type":"output_text"}}`,
			},
		},
		{
			name: "text stop without start",
			chunks: []string{
				`data: {"type":"response.content_part.done","part":{"type":"output_text"}}`,
			},
		},
		{
			name: "function argument events without function start",
			chunks: []string{
				`data: {"type":"response.function_call_arguments.delta","delta":"{}","output_index":2}`,
				`data: {"type":"response.function_call_arguments.done","arguments":"{}","output_index":2}`,
				`data: {"type":"response.output_item.done","item":{"type":"function_call","call_id":"call_missing","name":"Read"},"output_index":2}`,
			},
		},
		{
			name: "empty tool name is ignored",
			chunks: []string{
				`data: {"type":"response.output_item.added","item":{"type":"function_call","call_id":"call_empty","name":"   "},"output_index":2}`,
				`data: {"type":"response.function_call_arguments.delta","delta":"{}","output_index":2}`,
				`data: {"type":"response.output_item.done","item":{"type":"function_call","call_id":"call_empty","name":"   "},"output_index":2}`,
				`data: {"type":"response.completed","response":{"usage":{}}}`,
			},
			check: func(t *testing.T, joined string) {
				if strings.Contains(joined, `"type":"tool_use"`) {
					t.Fatalf("empty tool name emitted tool_use\n%s", joined)
				}
				if !strings.Contains(joined, `"stop_reason":"end_turn"`) {
					t.Fatalf("invalid tool call forced tool_use stop reason\n%s", joined)
				}
			},
		},
		{
			name: "missing call id uses item id",
			chunks: []string{
				`data: {"type":"response.output_item.added","item":{"type":"function_call","id":"fc_only","name":"Read"},"output_index":2}`,
				`data: {"type":"response.output_item.done","item":{"type":"function_call","id":"fc_only","name":"Read","arguments":"{}"},"output_index":2}`,
			},
			check: func(t *testing.T, joined string) {
				if !strings.Contains(joined, `"id":"fc_only"`) {
					t.Fatalf("item id was not used as tool_use id\n%s", joined)
				}
			},
		},
		{
			name: "missing all tool ids is ignored",
			chunks: []string{
				`data: {"type":"response.output_item.added","item":{"type":"function_call","name":"Read"},"output_index":2}`,
				`data: {"type":"response.output_item.done","item":{"type":"function_call","name":"Read"},"output_index":2}`,
			},
			check: func(t *testing.T, joined string) {
				if strings.Contains(joined, `"type":"tool_use"`) {
					t.Fatalf("tool call without an id emitted tool_use\n%s", joined)
				}
			},
		},
		{
			name: "double function stop",
			chunks: []string{
				`data: {"type":"response.output_item.added","item":{"type":"function_call","id":"fc_1","call_id":"call_1","name":"Read"},"output_index":2}`,
				`data: {"type":"response.output_item.done","item":{"type":"function_call","id":"fc_1","call_id":"call_1","name":"Read","arguments":"{}"},"output_index":2}`,
				`data: {"type":"response.output_item.done","item":{"type":"function_call","id":"fc_1","call_id":"call_1","name":"Read","arguments":"{}"},"output_index":2}`,
			},
		},
		{
			name: "late arguments after function stop",
			chunks: []string{
				`data: {"type":"response.output_item.added","item":{"type":"function_call","id":"fc_1","call_id":"call_1","name":"Read"},"output_index":2}`,
				`data: {"type":"response.output_item.done","item":{"type":"function_call","id":"fc_1","call_id":"call_1","name":"Read","arguments":"{}"},"output_index":2}`,
				`data: {"type":"response.function_call_arguments.done","item_id":"fc_1","arguments":"{\"late\":true}","output_index":2}`,
			},
			check: func(t *testing.T, joined string) {
				if strings.Contains(joined, `late`) {
					t.Fatalf("late arguments were emitted after tool block stop\n%s", joined)
				}
			},
		},
		{
			name: "mismatched function argument index",
			chunks: []string{
				`data: {"type":"response.output_item.added","item":{"type":"function_call","id":"fc_1","call_id":"call_1","name":"Read"},"output_index":2}`,
				`data: {"type":"response.function_call_arguments.delta","item_id":"fc_1","delta":"{\"wrong\":true}","output_index":3}`,
				`data: {"type":"response.output_item.done","item":{"type":"function_call","id":"fc_1","call_id":"call_1","name":"Read","arguments":"{}"},"output_index":2}`,
			},
			check: func(t *testing.T, joined string) {
				if strings.Contains(joined, `wrong`) {
					t.Fatalf("arguments for another output item were attached to the open tool block\n%s", joined)
				}
			},
		},
		{
			name: "terminal response closes open block",
			chunks: []string{
				`data: {"type":"response.output_item.added","item":{"type":"function_call","id":"fc_1","call_id":"call_1","name":"Read"},"output_index":2}`,
				`data: {"type":"response.completed","response":{"usage":{}}}`,
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			outputs := convertCodexClaudeStream(t, []byte(`{"tools":[{"name":"Read"}]}`), tt.chunks)
			assertClaudeContentBlockLifecycle(t, outputs)
			joined := strings.Join(outputs, "")
			assertNoInvalidClaudeToolUse(t, joined)
			if tt.check != nil {
				tt.check(t, joined)
			}
		})
	}
}

func TestConvertCodexResponseToClaude_ValidOrderedStreamRemainsStable(t *testing.T) {
	chunks := []string{
		`data: {"type":"response.created","response":{"id":"resp_1","model":"grok-4"}}`,
		`data: {"type":"response.reasoning_summary_part.added"}`,
		`data: {"type":"response.reasoning_summary_text.delta","delta":"think"}`,
		`data: {"type":"response.reasoning_summary_part.done"}`,
		`data: {"type":"response.content_part.added","part":{"type":"output_text"}}`,
		`data: {"type":"response.output_text.delta","delta":"answer"}`,
		`data: {"type":"response.content_part.done","part":{"type":"output_text"}}`,
		`data: {"type":"response.output_item.added","item":{"type":"function_call","id":"fc_1","call_id":"call_1","name":"Read"},"output_index":2}`,
		`data: {"type":"response.function_call_arguments.delta","item_id":"fc_1","delta":"{}","output_index":2}`,
		`data: {"type":"response.function_call_arguments.done","item_id":"fc_1","arguments":"{}","output_index":2}`,
		`data: {"type":"response.output_item.done","item":{"type":"function_call","id":"fc_1","call_id":"call_1","name":"Read","arguments":"{}"},"output_index":2}`,
		`data: {"type":"response.completed","response":{"usage":{"input_tokens":3,"output_tokens":4}}}`,
	}

	outputs := convertCodexClaudeStream(t, []byte(`{"tools":[{"name":"Read"}]}`), chunks)
	assertClaudeContentBlockLifecycle(t, outputs)

	got := claudeContentEventSummary(outputs)
	want := []string{
		"start:0:thinking",
		"delta:0:thinking_delta",
		"stop:0",
		"start:1:text",
		"delta:1:text_delta",
		"stop:1",
		"start:2:tool_use",
		"delta:2:input_json_delta",
		"delta:2:input_json_delta",
		"stop:2",
	}
	if strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Fatalf("content event summary changed\ngot:  %v\nwant: %v", got, want)
	}
}

func TestConvertCodexResponseToClaudeNonStream_SkipsInvalidToolCalls(t *testing.T) {
	response := []byte(`{
		"type":"response.completed",
		"response":{
			"id":"resp_1",
			"model":"grok-4",
			"stop_reason":"tool_use",
			"usage":{},
			"output":[
				{"type":"function_call","call_id":"call_empty","name":"   ","arguments":"{}"},
				{"type":"function_call","name":"Read","arguments":"{}"}
			]
		}
	}`)

	out := ConvertCodexResponseToClaudeNonStream(context.Background(), "", []byte(`{"tools":[{"name":"Read"}]}`), nil, response, nil)
	if strings.Contains(out, `"type":"tool_use"`) {
		t.Fatalf("invalid function calls emitted tool_use: %s", out)
	}
	if got := gjson.Get(out, "stop_reason").String(); got != "end_turn" {
		t.Fatalf("stop_reason = %q, want end_turn: %s", got, out)
	}
}

func convertCodexClaudeStream(t *testing.T, originalRequest []byte, chunks []string) []string {
	t.Helper()
	var param any
	outputs := make([]string, 0, len(chunks))
	for _, chunk := range chunks {
		outputs = append(outputs, ConvertCodexResponseToClaude(context.Background(), "", originalRequest, nil, []byte(chunk), &param)...)
	}
	return outputs
}

func assertClaudeContentBlockLifecycle(t *testing.T, outputs []string) {
	t.Helper()
	open := map[int]bool{}
	started := map[int]bool{}
	for outputIndex, output := range outputs {
		for _, line := range strings.Split(output, "\n") {
			if !strings.HasPrefix(line, "data: ") {
				continue
			}
			payload := gjson.Parse(strings.TrimPrefix(line, "data: "))
			index := int(payload.Get("index").Int())
			switch payload.Get("type").String() {
			case "content_block_start":
				if started[index] {
					t.Fatalf("output %d reused content block index %d: %s", outputIndex, index, line)
				}
				started[index] = true
				open[index] = true
			case "content_block_delta":
				if !open[index] {
					t.Fatalf("output %d emitted delta without an open start for index %d: %s", outputIndex, index, line)
				}
			case "content_block_stop":
				if !open[index] {
					t.Fatalf("output %d emitted stop without an open start for index %d: %s", outputIndex, index, line)
				}
				delete(open, index)
			case "message_stop":
				if len(open) != 0 {
					t.Fatalf("message_stop emitted with open content blocks %v", open)
				}
			}
		}
	}
}

func assertNoInvalidClaudeToolUse(t *testing.T, output string) {
	t.Helper()
	for _, line := range strings.Split(output, "\n") {
		if !strings.HasPrefix(line, "data: ") {
			continue
		}
		payload := gjson.Parse(strings.TrimPrefix(line, "data: "))
		if payload.Get("type").String() != "content_block_start" || payload.Get("content_block.type").String() != "tool_use" {
			continue
		}
		if strings.TrimSpace(payload.Get("content_block.name").String()) == "" {
			t.Fatalf("tool_use has empty name: %s", line)
		}
		if strings.TrimSpace(payload.Get("content_block.id").String()) == "" {
			t.Fatalf("tool_use has empty id: %s", line)
		}
	}
}

func claudeContentEventSummary(outputs []string) []string {
	var summary []string
	for _, output := range outputs {
		for _, line := range strings.Split(output, "\n") {
			if !strings.HasPrefix(line, "data: ") {
				continue
			}
			payload := gjson.Parse(strings.TrimPrefix(line, "data: "))
			index := int(payload.Get("index").Int())
			switch payload.Get("type").String() {
			case "content_block_start":
				summary = append(summary, fmt.Sprintf("start:%d:%s", index, payload.Get("content_block.type").String()))
			case "content_block_delta":
				summary = append(summary, fmt.Sprintf("delta:%d:%s", index, payload.Get("delta.type").String()))
			case "content_block_stop":
				summary = append(summary, fmt.Sprintf("stop:%d", index))
			}
		}
	}
	return summary
}
