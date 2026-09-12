package convert

import (
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"strings"
)

type AnthropicRequest struct {
	Model       string             `json:"model"`
	MaxTokens   int                `json:"max_tokens"`
	System      json.RawMessage    `json:"system"`
	Messages    []anthropicMessage `json:"messages"`
	Stream      bool               `json:"stream"`
	Temperature *float64           `json:"temperature"`
	Tools       []anthropicTool    `json:"tools"`
	ToolChoice  json.RawMessage    `json:"tool_choice"`
	Thinking    json.RawMessage    `json:"thinking"`
}

type anthropicMessage struct {
	Role    string          `json:"role"`
	Content json.RawMessage `json:"content"`
}

type anthropicTool struct {
	Name        string         `json:"name"`
	Description string         `json:"description"`
	InputSchema map[string]any `json:"input_schema"`
}

func FromAnthropic(raw []byte) (Request, error) {
	var in AnthropicRequest
	if err := json.Unmarshal(raw, &in); err != nil {
		return Request{}, fmt.Errorf("invalid anthropic request: %w", err)
	}
	if len(in.Messages) == 0 {
		return Request{}, fmt.Errorf("messages is required")
	}

	req := Request{
		Model:       in.Model,
		System:      anthropicSystem(in.System),
		MaxTokens:   clampMaxTokens(in.MaxTokens),
		Stream:      in.Stream,
		Temperature: in.Temperature,
	}

	toolNames := map[string]string{}
	for _, m := range in.Messages {
		blocks := anthropicBlocks(m.Content)
		switch m.Role {
		case "assistant":
			msg := Message{Role: "assistant"}
			for _, b := range blocks {
				switch b["type"] {
				case "text":
					if t, ok := b["text"].(string); ok {
						msg.Text += t
					}
				case "thinking":
					if t, ok := b["thinking"].(string); ok {
						msg.Reasoning += t
					}
				case "tool_use":
					id, _ := b["id"].(string)
					name, _ := b["name"].(string)
					toolNames[id] = name
					input, _ := json.Marshal(b["input"])
					if !json.Valid(input) {
						input = []byte("{}")
					}
					msg.ToolCalls = append(msg.ToolCalls, ToolCall{ID: id, Name: name, Input: input})
				}
			}
			req.Messages = append(req.Messages, msg)
		default:
			var text strings.Builder
			var images []Image
			var toolResults []Message
			for _, b := range blocks {
				switch b["type"] {
				case "text":
					if t, ok := b["text"].(string); ok {
						text.WriteString(t)
					}
				case "image":
					if img, ok := anthropicImage(b); ok {
						images = append(images, img)
					}
				case "tool_result":
					id, _ := b["tool_use_id"].(string)
					name := toolNames[id]
					toolResults = append(toolResults, Message{
						Role:       "tool",
						ToolCallID: id,
						ToolName:   name,
						ToolResult: anthropicToolResult(b["content"]),
					})
				}
			}
			req.Messages = append(req.Messages, toolResults...)
			if text.Len() > 0 || len(images) > 0 {
				req.Messages = append(req.Messages, Message{Role: "user", Text: text.String(), Images: images})
			}
		}
	}

	for _, t := range in.Tools {
		req.Tools = append(req.Tools, Tool{Name: t.Name, Description: t.Description, InputSchema: t.InputSchema})
	}
	if len(in.ToolChoice) > 0 && string(in.ToolChoice) != "null" {
		req.ToolChoice = mapAnthropicToolChoice(in.ToolChoice)
	}
	req.ReasoningEffort = mapThinking(in.Thinking)
	return req, nil
}

func anthropicSystem(raw json.RawMessage) string {
	if len(raw) == 0 || string(raw) == "null" {
		return ""
	}
	var s string
	if err := json.Unmarshal(raw, &s); err == nil {
		return s
	}
	return textFromContent(raw)
}

func anthropicBlocks(raw json.RawMessage) []map[string]any {
	if len(raw) == 0 || string(raw) == "null" {
		return nil
	}
	var s string
	if err := json.Unmarshal(raw, &s); err == nil {
		return []map[string]any{{"type": "text", "text": s}}
	}
	var parts []map[string]any
	_ = json.Unmarshal(raw, &parts)
	return parts
}

func anthropicToolResult(v any) string {
	switch t := v.(type) {
	case string:
		return t
	case []any:
		var b strings.Builder
		for _, item := range t {
			if m, ok := item.(map[string]any); ok {
				if s, ok := m["text"].(string); ok {
					b.WriteString(s)
				}
			}
		}
		return b.String()
	default:
		if v == nil {
			return ""
		}
		b, _ := json.Marshal(v)
		return string(b)
	}
}

func anthropicImage(b map[string]any) (Image, bool) {
	src, _ := b["source"].(map[string]any)
	if src == nil {
		if s, ok := b["image"].(string); ok {
			return Image{URL: s}, true
		}
		return Image{}, false
	}
	media, _ := src["media_type"].(string)
	data, _ := src["data"].(string)
	if data == "" {
		return Image{}, false
	}
	if media == "" {
		media = "image/png"
	}
	return Image{URL: "data:" + media + ";base64," + data, MIMEType: media}, true
}

func mapAnthropicToolChoice(raw json.RawMessage) any {
	obj := asObject(raw)
	if obj == nil {
		return nil
	}
	switch obj["type"] {
	case "auto", "none", "any":
		return map[string]any{"type": obj["type"]}
	case "tool":
		return map[string]any{"type": "tool", "name": obj["name"]}
	default:
		return obj
	}
}

func mapThinking(raw json.RawMessage) string {
	obj := asObject(raw)
	if obj == nil {
		return ""
	}
	switch obj["type"] {
	case "disabled", "none":
		return ""
	case "adaptive":
		if s, ok := obj["effort"].(string); ok {
			return s
		}
		return "medium"
	}
	budget, _ := obj["budget_tokens"].(float64)
	switch {
	case budget >= 10000:
		return "high"
	case budget >= 5000:
		return "medium"
	case budget > 0:
		return "low"
	default:
		return ""
	}
}

// FakeThinkingSignature satisfies Claude Code's shallow signature check.
func FakeThinkingSignature(thinking string) string {
	sum := sha256.Sum256([]byte(thinking))
	raw := append([]byte{0x12, byte(len(sum))}, sum[:]...)
	return base64.StdEncoding.EncodeToString(raw)
}

func AnthropicStopReason(finish string) string {
	switch finish {
	case "tool_calls", "tool-calls", "tool_use":
		return "tool_use"
	case "length", "max_tokens":
		return "max_tokens"
	default:
		return "end_turn"
	}
}
