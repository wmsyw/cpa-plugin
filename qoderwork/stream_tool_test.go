package main

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestCleanChunkJSONPreservesToolCallFragments(t *testing.T) {
	t.Parallel()

	chunk := `{"choices":[{"index":0,"delta":{"tool_calls":[{"index":0,"id":"call_1","type":"function","function":{"name":"context_probe","arguments":"{\\\"value\\\":"}}]}}]}`
	if got := cleanChunkJSON(chunk); got != chunk {
		t.Fatalf("tool-call fragment changed\ngot:  %s\nwant: %s", got, chunk)
	}
}

func TestCleanChunkJSONPreservesFinishReason(t *testing.T) {
	t.Parallel()

	chunk := `{"choices":[{"index":0,"delta":{"tool_calls":[],"function_call":null},"finish_reason":"tool_calls"}]}`
	cleaned := cleanChunkJSON(chunk)
	var payload struct {
		Choices []struct {
			Delta        map[string]any `json:"delta"`
			FinishReason string         `json:"finish_reason"`
		} `json:"choices"`
	}
	if err := json.Unmarshal([]byte(cleaned), &payload); err != nil {
		t.Fatalf("decode cleaned chunk: %v", err)
	}
	if payload.Choices[0].FinishReason != "tool_calls" {
		t.Fatalf("finish_reason = %q", payload.Choices[0].FinishReason)
	}
	if len(payload.Choices[0].Delta) != 0 {
		t.Fatalf("terminal delta = %#v, want empty", payload.Choices[0].Delta)
	}
}

func TestAggregateCompletionMergesFragmentedToolCall(t *testing.T) {
	t.Parallel()

	stream := strings.Join([]string{
		`data: {"id":"chatcmpl-1","model":"qmodel_preview","choices":[{"index":0,"delta":{"role":"assistant","content":""}}]}`,
		`data: {"choices":[{"index":0,"delta":{"tool_calls":[{"index":0,"id":"call_1","type":"function","function":{"name":"context_","arguments":"{\"value\":"}}]}}]}`,
		`data: {"choices":[{"index":0,"delta":{"tool_calls":[{"index":0,"function":{"name":"probe","arguments":"7}"}}]}}]}`,
		`data: {"choices":[{"index":0,"delta":{},"finish_reason":"tool_calls"}]}`,
		`data: [DONE]`,
	}, "\n\n")
	raw, err := aggregateCompletion(strings.NewReader(stream), "fallback")
	if err != nil {
		t.Fatalf("aggregateCompletion() error = %v", err)
	}
	var completion struct {
		Choices []struct {
			FinishReason string `json:"finish_reason"`
			Message      struct {
				ToolCalls []struct {
					ID       string `json:"id"`
					Function struct {
						Name      string `json:"name"`
						Arguments string `json:"arguments"`
					} `json:"function"`
				} `json:"tool_calls"`
			} `json:"message"`
		} `json:"choices"`
	}
	if err := json.Unmarshal(raw, &completion); err != nil {
		t.Fatalf("decode completion: %v", err)
	}
	choice := completion.Choices[0]
	if choice.FinishReason != "tool_calls" {
		t.Fatalf("finish_reason = %q", choice.FinishReason)
	}
	if len(choice.Message.ToolCalls) != 1 {
		t.Fatalf("tool call count = %d", len(choice.Message.ToolCalls))
	}
	call := choice.Message.ToolCalls[0]
	if call.ID != "call_1" || call.Function.Name != "context_probe" || call.Function.Arguments != `{"value":7}` {
		t.Fatalf("merged tool call = %#v", call)
	}
}

func TestClientNeedsSSEFrame(t *testing.T) {
	t.Parallel()

	if clientNeedsSSEFrame(map[string]any{"request_path": "/v1/chat/completions"}) {
		t.Fatal("chat-completions path should rely on host SSE framing")
	}
	if !clientNeedsSSEFrame(map[string]any{"request_path": "/v1/messages"}) {
		t.Fatal("Claude messages path requires plugin SSE framing")
	}
}
