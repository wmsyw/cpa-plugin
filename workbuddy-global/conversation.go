package main

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"
)

// resolveConversationID extracts a stable conversation identifier from
// client headers, request payload, or deterministically from the conversation root
// (hash of system prompt + initial user prompt).
//
// In multi-turn chat, keeping X-Conversation-ID / X-Session-ID / prompt_cache_key
// stable across turns is REQUIRED for upstream KV cache (Prompt Cache) affinity
// on Tencent Hunyuan / WorkBuddy. Randomizing or omitting it destroys instance
// affinity and drops cache hit rate to ~0-30%.
func resolveConversationID(clientHeaders http.Header, body []byte) string {
	// 1. Explicit client headers
	if clientHeaders != nil {
		for _, h := range []string{"X-Conversation-ID", "X-Session-ID", "Session-ID", "Conversation-ID"} {
			if v := strings.TrimSpace(clientHeaders.Get(h)); v != "" {
				return normalizeConversationID(v)
			}
		}
	}

	// 2. Explicit top-level payload fields
	if len(body) > 0 {
		var probe struct {
			ConversationID string `json:"conversation_id"`
			SessionID      string `json:"session_id"`
			PromptCacheKey string `json:"prompt_cache_key"`
		}
		if json.Unmarshal(body, &probe) == nil {
			if v := strings.TrimSpace(probe.ConversationID); v != "" {
				return normalizeConversationID(v)
			}
			if v := strings.TrimSpace(probe.SessionID); v != "" {
				return normalizeConversationID(v)
			}
			if v := strings.TrimSpace(probe.PromptCacheKey); v != "" {
				return normalizeConversationID(v)
			}
		}
	}

	// 3. Deterministic root-turn fingerprint (hash of system + first user prompt)
	if len(body) > 0 {
		var chat struct {
			Messages []map[string]any `json:"messages"`
		}
		if json.Unmarshal(body, &chat) == nil && len(chat.Messages) > 0 {
			var root strings.Builder
			for _, m := range chat.Messages {
				role, _ := m["role"].(string)
				role = strings.ToLower(strings.TrimSpace(role))
				if role == "system" || role == "developer" || role == "user" {
					extractTextContent(&root, m["content"])
					root.WriteString("\n---\n")
					if role == "user" {
						// Captured up to the first user message
						break
					}
				}
			}
			if root.Len() > 0 {
				sum := sha256.Sum256([]byte(root.String()))
				return hex.EncodeToString(sum[:16]) // 32-char hex
			}
		}
	}

	return randomHex(16)
}

func extractTextContent(b *strings.Builder, content any) {
	switch v := content.(type) {
	case string:
		b.WriteString(v)
	case []any:
		for _, part := range v {
			if p, ok := part.(map[string]any); ok {
				if t, ok := p["text"].(string); ok {
					b.WriteString(t)
				}
			}
		}
	}
}

func normalizeConversationID(v string) string {
	clean := strings.ToLower(strings.TrimSpace(v))
	clean = strings.ReplaceAll(clean, "-", "")
	if len(clean) >= 32 {
		return clean[:32]
	}
	if len(clean) > 0 {
		sum := sha256.Sum256([]byte(clean))
		return hex.EncodeToString(sum[:16])
	}
	return randomHex(16)
}

// randomHex returns n random bytes hex-encoded. CodeBuddy conversation/request
// IDs are 32-char hex strings; this keeps them UUID-free but stable enough.
func randomHex(n int) string {
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		return fmt.Sprintf("%0*x", n*2, time.Now().UnixNano())
	}
	return hex.EncodeToString(b)
}
