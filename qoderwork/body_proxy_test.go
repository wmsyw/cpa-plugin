package main

import (
	"encoding/json"
	"reflect"
	"testing"
)

func decodePayload(t *testing.T, raw []byte) map[string]any {
	t.Helper()
	var payload map[string]any
	if err := json.Unmarshal(raw, &payload); err != nil {
		t.Fatalf("decode payload: %v", err)
	}
	return payload
}

func decodeRawList(t *testing.T, values []json.RawMessage) []any {
	t.Helper()
	out := make([]any, 0, len(values))
	for _, value := range values {
		var decoded any
		if err := json.Unmarshal(value, &decoded); err != nil {
			t.Fatalf("decode raw JSON: %v", err)
		}
		out = append(out, decoded)
	}
	return out
}

func TestProxyModePreservesCallerMessagesAndTools(t *testing.T) {
	t.Parallel()

	messages := []json.RawMessage{
		json.RawMessage(`{"role":"system","name":"outer","content":"You are OUTER_AGENT","extension":{"keep":true}}`),
		json.RawMessage(`{"role":"user","content":[{"type":"text","text":"Use the probe"},{"type":"image_url","image_url":{"url":"data:image/png;base64,AA=="}}]}`),
		json.RawMessage(`{"role":"assistant","content":null,"tool_calls":[{"id":"call_1","type":"function","function":{"name":"context_probe","arguments":"{\"value\":7}"}}]}`),
		json.RawMessage(`{"role":"tool","tool_call_id":"call_1","name":"context_probe","content":"RESULT=14"}`),
		json.RawMessage(`{"role":"user","content":"Return the result"}`),
	}
	tools := json.RawMessage(`[{"type":"function","function":{"name":"context_probe","description":"Probe context","parameters":{"type":"object","properties":{"value":{"type":"integer"}},"required":["value"]}}}]`)
	toolChoice := json.RawMessage(`{"type":"function","function":{"name":"context_probe"}}`)

	raw, err := buildQoderBodyForMode(&openAIRequest{
		Messages:            messages,
		Tools:               tools,
		ToolChoice:          toolChoice,
		MaxCompletionTokens: 1234,
		ReasoningEffort:     "high",
	}, "qmodel_38max", "personal_standard", promptModeProxy)
	if err != nil {
		t.Fatalf("buildQoderBodyForMode() error = %v", err)
	}
	payload := decodePayload(t, raw)

	if got, want := payload["messages"], decodeRawList(t, messages); !reflect.DeepEqual(got, want) {
		t.Fatalf("messages changed\ngot:  %#v\nwant: %#v", got, want)
	}
	var wantTools any
	if err := json.Unmarshal(tools, &wantTools); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(payload["tools"], wantTools) {
		t.Fatalf("tools changed\ngot:  %#v\nwant: %#v", payload["tools"], wantTools)
	}
	parameters, _ := payload["parameters"].(map[string]any)
	if parameters["max_tokens"] != float64(1234) {
		t.Fatalf("max_tokens = %#v", parameters["max_tokens"])
	}
	if parameters["reasoning_effort"] != "xhigh" {
		t.Fatalf("reasoning_effort = %#v", parameters["reasoning_effort"])
	}
	if parameters["context_length"] != float64(oneMillionContextWindow) {
		t.Fatalf("context_length = %#v", parameters["context_length"])
	}
	var wantChoice any
	if err := json.Unmarshal(toolChoice, &wantChoice); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(parameters["tool_choice"], wantChoice) {
		t.Fatalf("tool_choice changed\ngot:  %#v\nwant: %#v", parameters["tool_choice"], wantChoice)
	}
}

func TestProxyModeRemovesEmbeddedPromptAndTools(t *testing.T) {
	t.Parallel()

	message := json.RawMessage(`{"role":"user","content":"hello"}`)
	raw, err := buildQoderBodyForMode(&openAIRequest{Messages: []json.RawMessage{message}}, "kmodel", "personal_standard", promptModeProxy)
	if err != nil {
		t.Fatalf("buildQoderBodyForMode() error = %v", err)
	}
	payload := decodePayload(t, raw)
	if _, exists := payload["tools"]; exists {
		t.Fatal("proxy mode retained embedded tools")
	}
	messages, _ := payload["messages"].([]any)
	if len(messages) != 1 {
		t.Fatalf("message count = %d, want 1", len(messages))
	}
	first, _ := messages[0].(map[string]any)
	if first["role"] != "user" {
		t.Fatalf("first role = %#v", first["role"])
	}
}

func TestNativeModeRetainsEmbeddedPromptAndTools(t *testing.T) {
	t.Parallel()

	message := json.RawMessage(`{"role":"user","content":"hello","extension":{"keep":true}}`)
	raw, err := buildQoderBodyForMode(&openAIRequest{Messages: []json.RawMessage{message}}, "kmodel", "personal_standard", promptModeNative)
	if err != nil {
		t.Fatalf("buildQoderBodyForMode() error = %v", err)
	}
	payload := decodePayload(t, raw)
	tools, _ := payload["tools"].([]any)
	if len(tools) != 14 {
		t.Fatalf("embedded tool count = %d, want 14", len(tools))
	}
	messages, _ := payload["messages"].([]any)
	if len(messages) != 2 {
		t.Fatalf("message count = %d, want 2", len(messages))
	}
	first, _ := messages[0].(map[string]any)
	if first["role"] != "system" {
		t.Fatalf("first role = %#v, want system", first["role"])
	}
	caller, _ := messages[1].(map[string]any)
	extension, _ := caller["extension"].(map[string]any)
	if extension["keep"] != true {
		t.Fatalf("caller extension lost: %#v", caller)
	}
}

func TestOfficialReasoningEffortMapping(t *testing.T) {
	t.Parallel()

	tests := []struct{ model, input, want string }{
		{model: "qmodel_38max", input: "low", want: "low"},
		{model: "qmodel_38max", input: "medium", want: "medium"},
		{model: "qmodel_38max", input: "high", want: "xhigh"},
		{model: "qmodel_38max", input: "max", want: "xhigh"},
		{model: "dmodel", input: "low", want: "high"},
		{model: "dfmodel", input: "max", want: "max"},
		{model: "gmodel", input: "max", want: "max"},
		{model: "gm51model", input: "medium", want: "high"},
		{model: "gm51model", input: "xhigh", want: "max"},
	}
	for _, tt := range tests {
		if got := mapOfficialReasoningEffort(tt.model, tt.input); got != tt.want {
			t.Errorf("mapOfficialReasoningEffort(%q, %q) = %q, want %q", tt.model, tt.input, got, tt.want)
		}
	}
}

func TestCurrentModelNameNormalization(t *testing.T) {
	t.Parallel()

	tests := map[string]string{
		"qwen3.8-max":   "qmodel_38max",
		"qmodel_38max":  "qmodel_38max",
		"qwen3.7-flash": "q37fmodel",
		"q37fmodel":     "q37fmodel",
		"glm-5.3":       "gmodel",
		"gmodel":        "gmodel",
	}
	for input, want := range tests {
		if got := cpaToUpstreamKey(input); got != want {
			t.Errorf("cpaToUpstreamKey(%q) = %q, want %q", input, got, want)
		}
	}
}

func TestExtractLatestUserPromptFromContentParts(t *testing.T) {
	t.Parallel()

	messages := []json.RawMessage{
		json.RawMessage(`{"role":"user","content":"older"}`),
		json.RawMessage(`{"role":"assistant","content":"response"}`),
		json.RawMessage(`{"role":"user","content":[{"type":"text","text":"first"},{"type":"image_url","image_url":{"url":"x"}},{"type":"input_text","text":"second"}]}`),
	}
	if got, want := extractLatestUserPrompt(messages), "first\nsecond"; got != want {
		t.Fatalf("extractLatestUserPrompt() = %q, want %q", got, want)
	}
}

func TestPromptModeNormalization(t *testing.T) {
	t.Parallel()

	for input, want := range map[string]string{
		"proxy":  promptModeProxy,
		"native": promptModeNative,
		"NATIVE": promptModeNative,
		"":       promptModeProxy,
		"other":  promptModeProxy,
	} {
		if got := normalizePromptMode(input); got != want {
			t.Errorf("normalizePromptMode(%q) = %q, want %q", input, got, want)
		}
	}
}
