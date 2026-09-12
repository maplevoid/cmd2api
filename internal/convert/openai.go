package convert

import (
	"encoding/json"
	"fmt"
	"strings"
)

type OpenAIChatRequest struct {
	Model               string          `json:"model"`
	Messages            []openAIMessage `json:"messages"`
	MaxTokens           *int            `json:"max_tokens"`
	MaxCompletionTokens *int            `json:"max_completion_tokens"`
	Temperature         *float64        `json:"temperature"`
	Stream              bool            `json:"stream"`
	Tools               []openAITool    `json:"tools"`
	ToolChoice          json.RawMessage `json:"tool_choice"`
	ParallelToolCalls   *bool           `json:"parallel_tool_calls"`
	ReasoningEffort     string          `json:"reasoning_effort"`
	PromptCacheKey      string          `json:"prompt_cache_key"`
}

type openAIMessage struct {
	Role             string           `json:"role"`
	Content          json.RawMessage  `json:"content"`
	Name             string           `json:"name"`
	ToolCallID       string           `json:"tool_call_id"`
	ToolCalls        []openAIToolCall `json:"tool_calls"`
	ReasoningContent string           `json:"reasoning_content"`
}

type openAIToolCall struct {
	ID       string `json:"id"`
	Type     string `json:"type"`
	Function struct {
		Name      string `json:"name"`
		Arguments string `json:"arguments"`
	} `json:"function"`
}

type openAITool struct {
	Type     string `json:"type"`
	Function struct {
		Name        string         `json:"name"`
		Description string         `json:"description"`
		Parameters  map[string]any `json:"parameters"`
	} `json:"function"`
	Name        string         `json:"name"`
	Description string         `json:"description"`
	InputSchema map[string]any `json:"input_schema"`
}

func FromOpenAI(raw []byte) (Request, error) {
	var in OpenAIChatRequest
	if err := json.Unmarshal(raw, &in); err != nil {
		return Request{}, fmt.Errorf("invalid openai request: %w", err)
	}
	if len(in.Messages) == 0 {
		return Request{}, fmt.Errorf("messages is required")
	}

	req := Request{
		Model:             in.Model,
		Stream:            in.Stream,
		Temperature:       in.Temperature,
		ReasoningEffort:   in.ReasoningEffort,
		PromptCacheKey:    in.PromptCacheKey,
		ParallelToolCalls: in.ParallelToolCalls,
	}
	if in.MaxTokens != nil {
		req.MaxTokens = clampMaxTokens(*in.MaxTokens)
	} else if in.MaxCompletionTokens != nil {
		req.MaxTokens = clampMaxTokens(*in.MaxCompletionTokens)
	}

	var systems []string
	for _, m := range in.Messages {
		switch m.Role {
		case "system", "developer":
			if t := textFromContent(m.Content); t != "" {
				systems = append(systems, t)
			}
		case "assistant":
			msg := Message{Role: "assistant", Text: textFromContent(m.Content), Reasoning: m.ReasoningContent}
			if msg.Reasoning == "" {
				msg.Reasoning = reasoningFromContent(m.Content)
			}
			for _, tc := range m.ToolCalls {
				args := json.RawMessage("{}")
				if strings.TrimSpace(tc.Function.Arguments) != "" && json.Valid([]byte(tc.Function.Arguments)) {
					args = json.RawMessage(tc.Function.Arguments)
				}
				msg.ToolCalls = append(msg.ToolCalls, ToolCall{
					ID:    tc.ID,
					Name:  tc.Function.Name,
					Input: args,
				})
			}
			req.Messages = append(req.Messages, msg)
		case "tool":
			req.Messages = append(req.Messages, Message{
				Role:       "tool",
				ToolCallID: m.ToolCallID,
				ToolName:   m.Name,
				ToolResult: textFromContent(m.Content),
			})
		default:
			msg := Message{Role: "user", Text: textFromContent(m.Content), Images: imagesFromContent(m.Content)}
			req.Messages = append(req.Messages, msg)
		}
	}
	req.System = strings.Join(systems, "\n")

	for _, t := range in.Tools {
		name := t.Function.Name
		desc := t.Function.Description
		schema := t.Function.Parameters
		if name == "" {
			name = t.Name
			desc = t.Description
			schema = t.InputSchema
		}
		req.Tools = append(req.Tools, Tool{Name: name, Description: desc, InputSchema: schema})
	}
	if len(in.ToolChoice) > 0 && string(in.ToolChoice) != "null" {
		req.ToolChoice = mapOpenAIToolChoice(in.ToolChoice)
	}
	return req, nil
}

func mapOpenAIToolChoice(raw json.RawMessage) any {
	var s string
	if err := json.Unmarshal(raw, &s); err == nil {
		switch s {
		case "required":
			return map[string]any{"type": "any"}
		case "none", "auto":
			return map[string]any{"type": s}
		default:
			return map[string]any{"type": "auto"}
		}
	}
	obj := asObject(raw)
	if obj == nil {
		return nil
	}
	if obj["type"] == "function" {
		if fn, ok := obj["function"].(map[string]any); ok {
			return map[string]any{"type": "tool", "name": fn["name"]}
		}
	}
	return obj
}

func imagesFromContent(raw json.RawMessage) []Image {
	var parts []map[string]any
	if err := json.Unmarshal(raw, &parts); err != nil {
		return nil
	}
	var out []Image
	for _, p := range parts {
		if p["type"] != "image_url" && p["type"] != "image" {
			continue
		}
		img := Image{}
		switch u := p["image_url"].(type) {
		case string:
			img.URL = u
		case map[string]any:
			if s, ok := u["url"].(string); ok {
				img.URL = s
			}
		}
		if s, ok := p["image"].(string); ok && img.URL == "" {
			img.URL = s
		}
		if s, ok := p["mimeType"].(string); ok {
			img.MIMEType = s
		}
		if img.URL != "" {
			out = append(out, img)
		}
	}
	return out
}

func reasoningFromContent(raw json.RawMessage) string {
	var parts []map[string]any
	if err := json.Unmarshal(raw, &parts); err != nil {
		return ""
	}
	var b strings.Builder
	for _, p := range parts {
		if p["type"] == "reasoning" || p["type"] == "thinking" {
			if t, ok := p["text"].(string); ok {
				b.WriteString(t)
			}
			if t, ok := p["thinking"].(string); ok {
				b.WriteString(t)
			}
		}
	}
	return b.String()
}
