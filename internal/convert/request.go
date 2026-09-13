package convert

import (
	"encoding/json"
	"strings"
	"time"

	"cmd2api/internal/id"
)

const DefaultMaxTokens = 64000
const MaxTokensCap = 200000
const DefaultModel = "deepseek/deepseek-v4-flash"

// Request is the protocol-neutral chat request used internally.
type Request struct {
	Model             string
	System            string
	Messages          []Message
	MaxTokens         int
	Temperature       *float64
	Stream            bool
	Tools             []Tool
	ToolChoice        any
	ParallelToolCalls *bool
	ReasoningEffort   string
	PromptCacheKey    string
}

type Message struct {
	Role       string // user, assistant, tool
	Text       string
	Reasoning  string
	Images     []Image
	ToolCalls  []ToolCall
	ToolCallID string
	ToolName   string
	ToolResult string
}

type Image struct {
	URL      string
	MIMEType string
}

type ToolCall struct {
	ID    string
	Name  string
	Input json.RawMessage
}

type Tool struct {
	Name        string
	Description string
	InputSchema map[string]any
}

// WireBody is the Command Code /alpha/generate payload.
type WireBody struct {
	Config         WireConfig `json:"config"`
	Memory         any        `json:"memory"`
	Taste          any        `json:"taste"`
	Skills         any        `json:"skills"`
	PermissionMode string     `json:"permissionMode"`
	ThreadID       string     `json:"threadId,omitempty"`
	Params         WireParams `json:"params"`
}

type WireConfig struct {
	WorkingDir    string   `json:"workingDir"`
	Date          string   `json:"date"`
	Environment   string   `json:"environment"`
	Structure     []any    `json:"structure"`
	IsGitRepo     bool     `json:"isGitRepo"`
	CurrentBranch string   `json:"currentBranch"`
	MainBranch    string   `json:"mainBranch"`
	GitStatus     string   `json:"gitStatus"`
	RecentCommits []string `json:"recentCommits"`
}

type WireParams struct {
	Model             string        `json:"model"`
	Messages          []WireMessage `json:"messages"`
	System            string        `json:"system"`
	MaxTokens         int           `json:"max_tokens"`
	Stream            bool          `json:"stream"`
	Temperature       *float64      `json:"temperature,omitempty"`
	ReasoningEffort   string        `json:"reasoning_effort,omitempty"`
	Tools             []WireTool    `json:"tools,omitempty"`
	ToolChoice        any           `json:"tool_choice,omitempty"`
	ParallelToolCalls *bool         `json:"parallel_tool_calls,omitempty"`
}

type WireMessage struct {
	Role    string     `json:"role"`
	Content []WirePart `json:"content"`
}

type WirePart struct {
	Type         string `json:"type"`
	Text         string `json:"text,omitempty"`
	Image        string `json:"image,omitempty"`
	MIMEType     string `json:"mimeType,omitempty"`
	ToolCallID   string `json:"toolCallId,omitempty"`
	ToolName     string `json:"toolName,omitempty"`
	Input        any    `json:"input,omitempty"`
	Output       any    `json:"output,omitempty"`
	CacheControl any    `json:"cache_control,omitempty"`
}

type WireTool struct {
	Name        string         `json:"name"`
	Description string         `json:"description"`
	InputSchema map[string]any `json:"input_schema"`
}

func (r Request) ToWire(workingDir, environment string) WireBody {
	model := r.Model
	if model == "" {
		model = DefaultModel
	}
	maxTokens := r.MaxTokens
	if maxTokens <= 0 {
		maxTokens = DefaultMaxTokens
	}
	if maxTokens > MaxTokensCap {
		maxTokens = MaxTokensCap
	}
	system := r.System
	if strings.TrimSpace(system) == "" {
		// Upstream injects a ~7.5k-token default prompt when system is omitted.
		system = " "
	}

	body := WireBody{
		Config: WireConfig{
			WorkingDir:    workingDir,
			Date:          time.Now().UTC().Format("2006-01-02"),
			Environment:   environment,
			Structure:     []any{},
			IsGitRepo:     false,
			RecentCommits: []string{},
		},
		PermissionMode: "standard",
		ThreadID:       id.New(),
		Params: WireParams{
			Model:       model,
			Messages:    wireMessages(r),
			System:      system,
			MaxTokens:   maxTokens,
			Stream:      true,
			Temperature: r.Temperature,
		},
	}
	if r.ReasoningEffort != "" {
		body.Params.ReasoningEffort = r.ReasoningEffort
	}
	if len(r.Tools) > 0 {
		tools := make([]WireTool, 0, len(r.Tools))
		for _, t := range r.Tools {
			schema := t.InputSchema
			if schema == nil {
				schema = map[string]any{"type": "object", "properties": map[string]any{}}
			}
			tools = append(tools, WireTool{Name: t.Name, Description: t.Description, InputSchema: schema})
		}
		body.Params.Tools = tools
	}
	if r.ToolChoice != nil {
		body.Params.ToolChoice = r.ToolChoice
	}
	if r.ParallelToolCalls != nil {
		body.Params.ParallelToolCalls = r.ParallelToolCalls
	}
	if r.PromptCacheKey != "" {
		markFirstUserCache(body.Params.Messages)
	}
	return body
}

func wireMessages(r Request) []WireMessage {
	toolNames := map[string]string{}
	for _, m := range r.Messages {
		for _, tc := range m.ToolCalls {
			if tc.ID != "" {
				toolNames[tc.ID] = tc.Name
			}
		}
	}

	out := make([]WireMessage, 0, len(r.Messages))
	for _, m := range r.Messages {
		switch m.Role {
		case "assistant":
			parts := make([]WirePart, 0, 4)
			if m.Reasoning != "" {
				parts = append(parts, WirePart{Type: "reasoning", Text: m.Reasoning})
			}
			if m.Text != "" {
				parts = append(parts, WirePart{Type: "text", Text: m.Text})
			}
			for _, tc := range m.ToolCalls {
				var input any = map[string]any{}
				if len(tc.Input) > 0 && json.Valid(tc.Input) {
					var v any
					if err := json.Unmarshal(tc.Input, &v); err == nil {
						input = v
					}
				}
				parts = append(parts, WirePart{Type: "tool-call", ToolCallID: tc.ID, ToolName: tc.Name, Input: input})
			}
			out = append(out, WireMessage{Role: "assistant", Content: parts})
		case "tool":
			name := m.ToolName
			if name == "" {
				name = toolNames[m.ToolCallID]
			}
			if name == "" {
				name = "unknown"
			}
			out = append(out, WireMessage{
				Role: "tool",
				Content: []WirePart{{
					Type:       "tool-result",
					ToolCallID: m.ToolCallID,
					ToolName:   name,
					Output:     map[string]any{"type": "text", "value": m.ToolResult},
				}},
			})
		default:
			parts := make([]WirePart, 0, 1+len(m.Images))
			if m.Text != "" || len(m.Images) == 0 {
				parts = append(parts, WirePart{Type: "text", Text: m.Text})
			}
			for _, img := range m.Images {
				parts = append(parts, WirePart{Type: "image", Image: img.URL, MIMEType: img.MIMEType})
			}
			out = append(out, WireMessage{Role: "user", Content: parts})
		}
	}
	return out
}

func markFirstUserCache(messages []WireMessage) {
	for i := range messages {
		if messages[i].Role != "user" {
			continue
		}
		for j := len(messages[i].Content) - 1; j >= 0; j-- {
			if messages[i].Content[j].Type != "text" {
				continue
			}
			messages[i].Content[j].CacheControl = map[string]any{"type": "ephemeral"}
			return
		}
	}
}

func asObject(raw json.RawMessage) map[string]any {
	if len(raw) == 0 {
		return nil
	}
	var v map[string]any
	if err := json.Unmarshal(raw, &v); err != nil {
		return nil
	}
	return v
}

func textFromContent(raw json.RawMessage) string {
	if len(raw) == 0 || string(raw) == "null" {
		return ""
	}
	var s string
	if err := json.Unmarshal(raw, &s); err == nil {
		return s
	}
	var parts []map[string]any
	if err := json.Unmarshal(raw, &parts); err == nil {
		var b strings.Builder
		for _, p := range parts {
			if t, ok := p["text"].(string); ok {
				if b.Len() > 0 {
					b.WriteByte('\n')
				}
				b.WriteString(t)
			}
		}
		return b.String()
	}
	return ""
}

func clampMaxTokens(n int) int {
	if n <= 0 {
		return DefaultMaxTokens
	}
	if n > MaxTokensCap {
		return MaxTokensCap
	}
	return n
}
