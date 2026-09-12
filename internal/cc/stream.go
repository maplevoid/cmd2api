package cc

import (
	"bufio"
	"encoding/json"
	"io"
	"strings"
)

type Event struct {
	Type               string          `json:"type"`
	Text               string          `json:"text"`
	Delta              string          `json:"delta"`
	ToolCallID         string          `json:"toolCallId"`
	ToolName           string          `json:"toolName"`
	Input              json.RawMessage `json:"input"`
	FinishReason       string          `json:"finishReason"`
	RawFinishReason    string          `json:"rawFinishReason"`
	Usage              *Usage          `json:"usage"`
	TotalUsage         *Usage          `json:"totalUsage"`
	Error              *EventError     `json:"error"`
	Message            string          `json:"message"`
	SystemPromptTokens int             `json:"systemPromptTokens"`
}

type EventError struct {
	Message string `json:"message"`
	Type    string `json:"type"`
}

type Usage struct {
	InputTokens       int                `json:"inputTokens"`
	OutputTokens      int                `json:"outputTokens"`
	CachedInputTokens int                `json:"cachedInputTokens"`
	InputTokenDetails *InputTokenDetails `json:"inputTokenDetails"`
}

type InputTokenDetails struct {
	NoCacheTokens    int `json:"noCacheTokens"`
	CacheReadTokens  int `json:"cacheReadTokens"`
	CacheWriteTokens int `json:"cacheWriteTokens"`
}

func (u *Usage) Normalize() {
	if u == nil {
		return
	}
	if u.OutputTokens == 0 {
		u.InputTokens = 0
		u.CachedInputTokens = 0
	}
}

func (u *Usage) CacheRead() int {
	if u == nil {
		return 0
	}
	if u.CachedInputTokens > 0 {
		return u.CachedInputTokens
	}
	if u.InputTokenDetails != nil {
		return u.InputTokenDetails.CacheReadTokens
	}
	return 0
}

func (u *Usage) CacheWrite() int {
	if u == nil || u.InputTokenDetails == nil {
		return 0
	}
	return u.InputTokenDetails.CacheWriteTokens
}

func (u *Usage) NoCache() int {
	if u == nil {
		return 0
	}
	if u.InputTokenDetails != nil && u.InputTokenDetails.NoCacheTokens > 0 {
		return u.InputTokenDetails.NoCacheTokens
	}
	v := u.InputTokens - u.CacheRead() - u.CacheWrite()
	if v < 0 {
		return 0
	}
	return v
}

func (e Event) TextDelta() string {
	if e.Text != "" {
		return e.Text
	}
	return e.Delta
}

func (e Event) UsageOrTotal() *Usage {
	if e.TotalUsage != nil {
		return e.TotalUsage
	}
	return e.Usage
}

func ReadEvents(r io.Reader, fn func(Event) error) error {
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 0, 64*1024), 8*1024*1024)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" || line == "[DONE]" || strings.HasPrefix(line, ":") {
			continue
		}
		var ev Event
		if err := json.Unmarshal([]byte(line), &ev); err != nil {
			continue
		}
		if ev.Type == "" {
			continue
		}
		if err := fn(ev); err != nil {
			return err
		}
	}
	return sc.Err()
}

func MapFinishReason(reason string) string {
	switch reason {
	case "tool-calls", "tool_use", "tool_calls":
		return "tool_calls"
	case "length", "max_tokens":
		return "length"
	case "stop", "end_turn", "":
		return "stop"
	default:
		return reason
	}
}
