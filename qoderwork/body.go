// body.go constructs the QoderWork agent_chat_generation request body from
// OpenAI-style chat completion inputs.
//
// The base template supplies the transport envelope. Proxy mode removes its
// Qoder-specific prompt/tools and forwards caller messages/tools losslessly;
// native mode retains the original Qoder agent prompt and tool catalog.
package main

import (
	"bytes"
	_ "embed"
	"encoding/json"
	"fmt"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/google/uuid"
)

//go:embed baseprompt.json
var basepromptJSON []byte

// cpaToUpstreamKey maps CPA-facing model names to upstream keys.
// Unknown names pass through unchanged (server silently routes to auto).
func cpaToUpstreamKey(cpaModel string) string {
	switch cpaModel {
	case "qoder-auto", "auto":
		return "auto"
	case "qwen3.8-max", "qmodel_38max":
		return "qmodel_38max"
	case "qwen3.7-max", "qmodel_latest":
		return "qmodel_latest"
	case "qwen3.7-plus", "qmodel":
		return "qmodel"
	case "qwen3.7-flash", "q37fmodel":
		return "q37fmodel"
	case "deepseek-v4-pro", "dmodel":
		return "dmodel"
	case "deepseek-v4-flash", "dfmodel":
		return "dfmodel"
	case "glm-5.3", "gmodel":
		return "gmodel"
	case "glm-5.2", "gm51model":
		return "gm51model"
	case "kimi-k2.7-code", "kmodel":
		return "kmodel"
	case "minimax-m2.7", "mmodel":
		return "mmodel"
	}
	return cpaModel
}

// openAIRequest preserves messages and tools as raw JSON so proxy mode does
// not drop tool_calls, tool_call_id, names, multimodal content, or extensions.
type openAIRequest struct {
	Model               string            `json:"model"`
	Messages            []json.RawMessage `json:"messages"`
	Stream              bool              `json:"stream"`
	Tools               json.RawMessage   `json:"tools"`
	ToolChoice          json.RawMessage   `json:"tool_choice"`
	MaxTokens           int64             `json:"max_tokens"`
	MaxCompletionTokens int64             `json:"max_completion_tokens"`
	ReasoningEffort     string            `json:"reasoning_effort"`
}

type messageProbe struct {
	Role    string          `json:"role"`
	Content json.RawMessage `json:"content"`
}

// extractLatestUserPrompt returns textual content from the last user message.
// It accepts both string content and OpenAI/Anthropic-style content-part arrays.
func extractLatestUserPrompt(messages []json.RawMessage) string {
	for i := len(messages) - 1; i >= 0; i-- {
		var message messageProbe
		if json.Unmarshal(messages[i], &message) != nil || !strings.EqualFold(message.Role, "user") {
			continue
		}
		if text := extractTextContent(message.Content); text != "" {
			return text
		}
	}
	return ""
}

func extractTextContent(content json.RawMessage) string {
	if len(content) == 0 || bytes.Equal(bytes.TrimSpace(content), []byte("null")) {
		return ""
	}
	var text string
	if json.Unmarshal(content, &text) == nil {
		return text
	}
	var parts []struct {
		Type string `json:"type"`
		Text string `json:"text"`
	}
	if json.Unmarshal(content, &parts) != nil {
		return ""
	}
	texts := make([]string, 0, len(parts))
	for _, part := range parts {
		if part.Text == "" {
			continue
		}
		switch strings.ToLower(strings.TrimSpace(part.Type)) {
		case "", "text", "input_text":
			texts = append(texts, part.Text)
		}
	}
	return strings.Join(texts, "\n")
}

func rawJSONPresent(value json.RawMessage) bool {
	trimmed := bytes.TrimSpace(value)
	return len(trimmed) > 0 && !bytes.Equal(trimmed, []byte("null"))
}

func appendNativeMessages(base map[string]any, caller []json.RawMessage) {
	messages := make([]any, 0, len(caller)+1)
	if embedded, ok := base["messages"].([]any); ok {
		for _, message := range embedded {
			m, ok := message.(map[string]any)
			if !ok {
				continue
			}
			if role, _ := m["role"].(string); role == "system" {
				messages = append(messages, message)
			}
		}
	}
	for _, message := range caller {
		messages = append(messages, message)
	}
	base["messages"] = messages
}

func applyProxyInputs(base map[string]any, req *openAIRequest) {
	base["messages"] = req.Messages
	if rawJSONPresent(req.Tools) {
		base["tools"] = req.Tools
	} else {
		delete(base, "tools")
	}

	parameters, ok := base["parameters"].(map[string]any)
	if !ok {
		parameters = make(map[string]any, 4)
		base["parameters"] = parameters
	}
	if rawJSONPresent(req.ToolChoice) {
		parameters["tool_choice"] = req.ToolChoice
	} else {
		delete(parameters, "tool_choice")
	}
}

func mapOfficialReasoningEffort(modelKey, effort string) string {
	effort = strings.ToLower(strings.TrimSpace(effort))
	switch modelKey {
	case "qmodel_38max":
		switch effort {
		case "none", "minimal", "low":
			return "low"
		case "medium":
			return "medium"
		case "high", "xhigh", "max":
			return "xhigh"
		}
	case "dmodel":
		if effort == "low" {
			return "high"
		}
	case "gm51model":
		switch effort {
		case "none", "minimal":
			return "none"
		case "low", "medium", "high":
			return "high"
		case "xhigh", "max":
			return "max"
		}
	}
	return effort
}

func applyRequestControls(base map[string]any, req *openAIRequest, modelKey string) {
	parameters, ok := base["parameters"].(map[string]any)
	if !ok {
		parameters = make(map[string]any, 4)
		base["parameters"] = parameters
	}
	if req.MaxCompletionTokens > 0 {
		parameters["max_tokens"] = req.MaxCompletionTokens
	} else if req.MaxTokens > 0 {
		parameters["max_tokens"] = req.MaxTokens
	}
	if effort := mapOfficialReasoningEffort(modelKey, req.ReasoningEffort); effort != "" {
		parameters["reasoning_effort"] = effort
	}
}

func truncateRunes(value string, limit int) string {
	if limit <= 0 || utf8.RuneCountInString(value) <= limit {
		return value
	}
	runes := []rune(value)
	return string(runes[:limit])
}

// buildQoderBody renders the upstream agent_chat_generation body for one request.
// modelKey is the upstream key (already mapped via cpaToUpstreamKey).
func buildQoderBody(req *openAIRequest, modelKey, userType string) ([]byte, error) {
	return buildQoderBodyForMode(req, modelKey, userType, loadedPromptMode())
}

func buildQoderBodyForMode(req *openAIRequest, modelKey, userType, mode string) ([]byte, error) {
	var base map[string]any
	if err := json.Unmarshal(basepromptJSON, &base); err != nil {
		return nil, fmt.Errorf("baseprompt decode: %w", err)
	}

	prompt := extractLatestUserPrompt(req.Messages)
	if prompt == "" {
		return nil, fmt.Errorf("no user message in request")
	}

	nid := uuid.NewString()
	base["request_id"] = nid
	base["chat_record_id"] = nid
	base["request_set_id"] = uuid.NewString()
	base["session_id"] = uuid.NewString()
	base["stream"] = true
	base["aliyun_user_type"] = userType
	base["agent_id"] = "agent_common"

	if modelConfig, ok := base["model_config"].(map[string]any); ok {
		modelConfig["key"] = modelKey
		applyPreferredContextWindow(base, modelKey)
	}

	if chatContext, ok := base["chat_context"].(map[string]any); ok {
		if text, ok := chatContext["text"].(map[string]any); ok {
			text["text"] = prompt
		}
		if extra, ok := chatContext["extra"].(map[string]any); ok {
			if original, ok := extra["originalContent"].(map[string]any); ok {
				original["text"] = prompt
			}
			if modelConfig, ok := extra["modelConfig"].(map[string]any); ok {
				modelConfig["key"] = modelKey
			}
		}
	}

	if normalizePromptMode(mode) == promptModeNative {
		appendNativeMessages(base, req.Messages)
	} else {
		applyProxyInputs(base, req)
	}
	applyRequestControls(base, req, modelKey)

	if business, ok := base["business"].(map[string]any); ok {
		business["id"] = uuid.NewString()
		business["begin_at"] = time.Now().UnixMilli()
		business["name"] = truncateRunes(prompt, 30)
	}

	return json.Marshal(base)
}
