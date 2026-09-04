// payload.go rewrites the outgoing chat completion request body before it's
// forwarded upstream. The single-pass entry point is prepareUpstreamBody; the
// four *InPlace helpers are the field-level mutations it composes, and the
// legacy *ForUpstream / forceStreamBody wrappers exist for tests and other
// call sites that need them individually.
package main

import (
	"encoding/json"
	"strings"
)

// forceStreamBody returns the request body with "stream":true set, since the
// upstream rejects non-streaming chat requests.
// prepareUpstreamBody composes forceStreamBody + normalizeToolsForUpstream +
// rewriteSystemForUpstream + ensureSystemMessage + rewriteModelInBody into a
// single unmarshal/marshal pass (v0.6.31 perf: was 4-5 full JSON round-trips
// on every chat completion). The 4 legacy helpers remain for tests and other
// call sites that need them individually.
func prepareUpstreamBody(payload, original []byte, sa *storedAuth, upstreamModel string, conversationID ...string) []byte {
	src := payload
	if len(src) == 0 {
		src = original
	}
	if len(src) == 0 {
		return src
	}
	var obj map[string]any
	if json.Unmarshal(src, &obj) != nil {
		return src
	}

	// 1. forceStream: CodeBuddy rejects non-stream requests.
	obj["stream"] = true

	// 2. normalizeTools: tool_choice object form → string; "none" suppresses tools.
	normalizeToolsInPlace(obj)

	// 3. normalize Global-only roles before inspecting their content. A
	// developer message has system semantics upstream and can carry the same
	// blocked client identity template.
	ensureSystemMessageInPlace(obj, sa)

	// 4. rewriteSystem: strip blocked Claude Code template phrases.
	rewriteSystemInPlace(obj)

	// 5. desensitize: insert zero-width separators into provider-blocked
	// terms across system/developer prompt text, marker-bearing user text,
	// and tool description/title fields (ported from Sliverkiss/cpa-plugin
	// v0.9.3). Runs after the role normalization and identity rewrite.
	applyDesensitizeInPlace(obj)

	// 6. rewriteModel and translate official reasoning efforts to upstream labels.
	rewriteModelInPlace(obj, upstreamModel)
	mapReasoningEffortInPlace(obj, upstreamModel)

	// 7. Inject prompt_cache_key for upstream KV Cache routing if missing
	if len(conversationID) > 0 && strings.TrimSpace(conversationID[0]) != "" {
		if _, ok := obj["prompt_cache_key"]; !ok {
			obj["prompt_cache_key"] = strings.TrimSpace(conversationID[0])
		}
	}

	out, err := json.Marshal(obj)
	if err != nil {
		return src
	}
	return out
}

// normalizeToolsInPlace is the in-place form of normalizeToolsForUpstream.
// Returns true when obj was modified.
func normalizeToolsInPlace(obj map[string]any) bool {
	changed := false
	suppressTools := func() {
		if _, ok := obj["tools"]; ok {
			delete(obj, "tools")
			changed = true
		}
		if _, ok := obj["functions"]; ok {
			delete(obj, "functions")
			changed = true
		}
	}
	if tc, present := obj["tool_choice"]; present {
		switch v := tc.(type) {
		case string:
			if strings.EqualFold(strings.TrimSpace(v), "none") {
				delete(obj, "tool_choice")
				suppressTools()
				changed = true
			}
		case map[string]any:
			typ, _ := v["type"].(string)
			typ = strings.ToLower(strings.TrimSpace(typ))
			switch typ {
			case "none":
				delete(obj, "tool_choice")
				suppressTools()
				changed = true
			case "auto", "required":
				obj["tool_choice"] = typ
				changed = true
			case "function":
				name := ""
				if fn, ok := v["function"].(map[string]any); ok {
					name, _ = fn["name"].(string)
				}
				if name == "" {
					name, _ = v["name"].(string)
				}
				name = strings.TrimSpace(name)
				if name != "" {
					obj["tool_choice"] = name
				} else {
					obj["tool_choice"] = "auto"
				}
				changed = true
			default:
				delete(obj, "tool_choice")
				changed = true
			}
		default:
			delete(obj, "tool_choice")
			changed = true
		}
	}
	return changed
}

// rewriteSystemInPlace removes only the two provider-rejected Claude Code
// identity markers from system and assistant context. User and tool content
// stays literal: it is data supplied to the model, not client identity.
func rewriteSystemInPlace(obj map[string]any) bool {
	messages, _ := obj["messages"].([]any)
	changed := false
	for _, m := range messages {
		msg, ok := m.(map[string]any)
		if !ok {
			continue
		}
		role, _ := msg["role"].(string)
		if rewriteContentField(msg, role) {
			changed = true
		}
	}
	return changed
}

// ensureSystemMessageInPlace normalizes Global messages to the subset the
// upstream chat gateway accepts: a leading system prompt followed by standard
// OpenAI chat roles. Global rejects the OpenAI-only "developer" role with
// code 11128 ("Illegal API invocation from an unapproved channel").
//
// The developer role has system-level semantics. Convert it to system rather
// than dropping or weakening its instruction, then preserve every message's
// relative order. If no system prompt is first after normalization, prepend the
// minimal stable prompt required by the Global gateway.
func ensureSystemMessageInPlace(obj map[string]any, sa *storedAuth) bool {
	if sa == nil || !isGlobalDomain(sa.Auth.Domain) {
		return false
	}
	messages, ok := obj["messages"].([]any)
	if !ok || len(messages) == 0 {
		return false
	}

	changed := false
	for _, message := range messages {
		msg, ok := message.(map[string]any)
		if !ok {
			continue
		}
		if role, _ := msg["role"].(string); strings.EqualFold(role, "developer") {
			msg["role"] = "system"
			changed = true
		}
	}
	if first, ok := messages[0].(map[string]any); ok {
		if role, _ := first["role"].(string); strings.EqualFold(role, "system") {
			return changed
		}
	}
	systemMsg := map[string]any{
		"role":    "system",
		"content": "You are a helpful assistant.",
	}
	obj["messages"] = append([]any{systemMsg}, messages...)
	return true
}

// rewriteModelInPlace swaps obj["model"] to upstreamModel when non-empty.
// Mirrors rewriteModelInBody's behavior (case-insensitive compare); returns
// true when modified.
func rewriteModelInPlace(obj map[string]any, upstreamModel string) bool {
	upstreamModel = strings.TrimSpace(upstreamModel)
	if upstreamModel == "" {
		return false
	}
	cur, _ := obj["model"].(string)
	if strings.EqualFold(strings.TrimSpace(cur), upstreamModel) {
		return false
	}
	obj["model"] = upstreamModel
	return true
}

func forceStreamBody(payload, original []byte) []byte {
	src := payload
	if len(src) == 0 {
		src = original
	}
	var obj map[string]any
	if json.Unmarshal(src, &obj) != nil {
		return src
	}
	obj["stream"] = true
	out, err := json.Marshal(obj)
	if err != nil {
		return src
	}
	return out
}

// normalizeToolsForUpstream adapts OpenAI tools / tool_choice fields to
// CodeBuddy's chat schema before the request is forwarded.
//
// Live-verified against /v2/chat/completions (2026-07):
//  1. tool_choice is typed as string on the upstream Go struct. OpenAI's object
//     form {"type":"function","function":{"name":"..."}} returns 400 code 11101
//     ("cannot unmarshal object into Go struct field Request.tool_choice of
//     type string"). Convert known object shapes to the matching string.
//  2. tool_choice "none" is accepted but ignored when tools[] is non-empty —
//     the model still emits tool_calls. The only reliable way to suppress tools
//     is to omit tools (and functions) entirely.
//
// String values auto / required / <function name> are left untouched.
func normalizeToolsForUpstream(payload []byte) []byte {
	if len(payload) == 0 {
		return payload
	}
	var obj map[string]any
	if json.Unmarshal(payload, &obj) != nil {
		return payload
	}
	changed := false

	suppressTools := func() {
		if _, ok := obj["tools"]; ok {
			delete(obj, "tools")
			changed = true
		}
		if _, ok := obj["functions"]; ok {
			delete(obj, "functions")
			changed = true
		}
	}

	if tc, present := obj["tool_choice"]; present {
		switch v := tc.(type) {
		case string:
			if strings.EqualFold(strings.TrimSpace(v), "none") {
				delete(obj, "tool_choice")
				suppressTools()
				changed = true
			}
		case map[string]any:
			typ, _ := v["type"].(string)
			typ = strings.ToLower(strings.TrimSpace(typ))
			switch typ {
			case "none":
				delete(obj, "tool_choice")
				suppressTools()
				changed = true
			case "auto", "required":
				obj["tool_choice"] = typ
				changed = true
			case "function":
				name := ""
				if fn, ok := v["function"].(map[string]any); ok {
					name, _ = fn["name"].(string)
				}
				if name == "" {
					name, _ = v["name"].(string)
				}
				name = strings.TrimSpace(name)
				if name != "" {
					obj["tool_choice"] = name
				} else {
					// Object force without a name: fall back to auto instead of 400.
					obj["tool_choice"] = "auto"
				}
				changed = true
			default:
				// Unknown object shape → drop rather than forward a 400.
				delete(obj, "tool_choice")
				changed = true
			}
		default:
			// null / array / number — drop to keep upstream happy.
			delete(obj, "tool_choice")
			changed = true
		}
	}

	if !changed {
		return payload
	}
	out, err := json.Marshal(obj)
	if err != nil {
		return payload
	}
	return out
}

// rewriteSystemForUpstream removes the rejected Claude Code identity markers
// from system and assistant context while preserving user and tool content.
func rewriteSystemForUpstream(payload []byte) []byte {
	if len(payload) == 0 {
		return payload
	}
	var obj map[string]any
	if json.Unmarshal(payload, &obj) != nil {
		return payload
	}
	if !rewriteSystemInPlace(obj) {
		return payload
	}
	out, err := json.Marshal(obj)
	if err != nil {
		return payload
	}
	return out
}

// ensureSystemMessage injects a minimal system message if none is present.
// Global (www.workbuddy.ai) rejects user-only requests with code 11101
// "Parse message failed: 11101:invalid request". CN (copilot.tencent.com)
// does not require a system message but tolerates one. Inserting a
// harmless system message unifies both paths.
func ensureSystemMessage(payload []byte, sa *storedAuth) []byte {
	if len(payload) == 0 {
		return payload
	}
	// Only inject for Global; CN doesn't need it and we minimize diff.
	if sa == nil || !isGlobalDomain(sa.Auth.Domain) {
		return payload
	}
	var obj map[string]any
	if json.Unmarshal(payload, &obj) != nil {
		return payload
	}
	messages, ok := obj["messages"].([]any)
	if !ok || len(messages) == 0 {
		return payload
	}
	for _, m := range messages {
		msg, ok := m.(map[string]any)
		if !ok {
			continue
		}
		if role, _ := msg["role"].(string); strings.EqualFold(role, "system") {
			return payload // already has system message
		}
	}
	systemMsg := map[string]any{
		"role":    "system",
		"content": "You are a helpful assistant.",
	}
	obj["messages"] = append([]any{systemMsg}, messages...)
	out, err := json.Marshal(obj)
	if err != nil {
		return payload
	}
	return out
}

const claudeCodeIdentity = "You are Claude Code, Anthropic's official CLI for Claude"

// rewriteContentField sanitizes a message's plain-string or multimodal text
// content according to its role. Only system and assistant messages carry
// client identity context that the Global gateway blocklists.
func rewriteContentField(msg map[string]any, role string) bool {
	rewrite := sanitizeContentForRole(role)
	if rewrite == nil {
		return false
	}
	switch c := msg["content"].(type) {
	case string:
		if r := rewrite(c); r != c {
			msg["content"] = r
			return true
		}
	case []any:
		modified := false
		for _, p := range c {
			part, ok := p.(map[string]any)
			if !ok {
				continue
			}
			if t, ok := part["text"].(string); ok {
				if r := rewrite(t); r != t {
					part["text"] = r
					modified = true
				}
			}
		}
		return modified
	}
	return false
}

func sanitizeContentForRole(role string) func(string) string {
	switch strings.ToLower(strings.TrimSpace(role)) {
	case "system":
		return sanitizeBlockedSystemTemplate
	case "assistant":
		return sanitizeBlockedAssistantTemplate
	default:
		return nil
	}
}

// sanitizeBlockedSystemTemplate replaces a rejected client-identity line at
// the start of a system prompt. The gateway applies this check to the whole
// first line, so drop its vendor-specific suffix rather than changing one word.
func sanitizeBlockedSystemTemplate(s string) string {
	if strings.HasPrefix(s, claudeCodeIdentity) {
		if newline := strings.IndexByte(s, '\n'); newline >= 0 {
			s = "You are an expert software engineering agent." + s[newline:]
		} else {
			s = "You are an expert software engineering agent."
		}
	}
	return strings.ReplaceAll(s,
		"Main branch (you will usually use this for PRs)",
		"Default branch (you will usually use this for PRs)")
}

// sanitizeBlockedAssistantTemplate removes the same client identity anywhere
// in assistant history. Keep the surrounding conversation verbatim.
func sanitizeBlockedAssistantTemplate(s string) string {
	return strings.ReplaceAll(s, claudeCodeIdentity, "You are an expert software engineering agent")
}

// sanitizeBlockedTemplates remains the system-template helper used by
// existing callers and tests.
func sanitizeBlockedTemplates(s string) string {
	return sanitizeBlockedSystemTemplate(s)
}

// mapReasoningEffortInPlace translates official-model effort names to the
// labels accepted by WorkBuddy. Clients can therefore consistently send the
// official values low/high/max.
func mapReasoningEffortInPlace(obj map[string]any, model string) bool {
	effort, _ := obj["reasoning_effort"].(string)
	effort = strings.ToLower(strings.TrimSpace(effort))
	if effort == "" {
		return false
	}
	mapped := effort
	switch model {
	case "glm-5.3", "glm-5.2":
		// Global GLM accepts xhigh instead of the canonical max label.
		if effort == "max" {
			mapped = "xhigh"
		}
	case "gpt-5.5", "gpt-5.4", "gpt-5.3-codex":
		// These models expose low/medium/high/xhigh; clamp max to xhigh.
		if effort == "max" {
			mapped = "xhigh"
		}
	case "gemini-3.5-flash":
		// Global Gemini exposes minimal/low/medium/high. Map the canonical
		// no-think and xhigh values to the supported endpoints.
		switch effort {
		case "none":
			mapped = "minimal"
		case "xhigh":
			mapped = "high"
		}
	case "hy3":
		// WorkBuddy's Hy3 route uses high for the deepest canonical setting.
		if effort == "max" {
			mapped = "high"
		}
	case "hy4-preview":
		// Hy4 Preview exposes only none/high: map the compatible OpenAI
		// ladder to its no-think/deep-think endpoints.
		switch effort {
		case "low":
			mapped = "none"
		case "medium", "xhigh", "max":
			mapped = "high"
		}
	case "gpt-5.6-sol", "gpt-5.6-terra", "gpt-5.6-luna", "kimi-k3", "kimi-k2.6", "minimax-m3":
		// These Global routes accept the complete canonical ladder unchanged.
	}
	if mapped == effort {
		return false
	}
	obj["reasoning_effort"] = mapped
	return true
}

// rewriteModelInBody replaces the "model" field of a chat-completions body
// with the resolved upstream model ID.
func rewriteModelInBody(body []byte, upstreamModel string) []byte {
	if len(body) == 0 || strings.TrimSpace(upstreamModel) == "" {
		return body
	}
	var obj map[string]any
	if json.Unmarshal(body, &obj) != nil {
		return body
	}
	cur, _ := obj["model"].(string)
	if strings.EqualFold(strings.TrimSpace(cur), strings.TrimSpace(upstreamModel)) {
		return body
	}
	obj["model"] = upstreamModel
	out, err := json.Marshal(obj)
	if err != nil {
		return body
	}
	return out
}

func isEmptyValue(v any) bool {
	switch x := v.(type) {
	case nil:
		return true
	case string:
		return x == ""
	case []any:
		return len(x) == 0
	case map[string]any:
		if len(x) == 0 {
			return true
		}
		// Legacy function_call shell: {"name":"","arguments":""} is the
		// upstream's terminal-chunk artifact, not a real call — treat as empty
		// when every value is itself empty.
		for _, val := range x {
			if !isEmptyValue(val) {
				return false
			}
		}
		return true
	}
	return false
}
