package convert

import "testing"

func TestFromOpenAIBasic(t *testing.T) {
	raw := []byte(`{
		"model": "deepseek/deepseek-v4-flash",
		"messages": [
			{"role": "system", "content": "be brief"},
			{"role": "user", "content": "hi"}
		],
		"max_tokens": 100
	}`)
	req, err := FromOpenAI(raw)
	if err != nil {
		t.Fatal(err)
	}
	if req.System != "be brief" {
		t.Fatalf("system=%q", req.System)
	}
	if len(req.Messages) != 1 || req.Messages[0].Text != "hi" {
		t.Fatalf("messages=%+v", req.Messages)
	}
	wire := req.ToWire("/tmp", "linux-amd64, Go")
	if wire.Params.System != "be brief" {
		t.Fatalf("wire system=%q", wire.Params.System)
	}
	if !wire.Params.Stream {
		t.Fatal("upstream must always stream")
	}
	if wire.PermissionMode != "standard" {
		t.Fatalf("permissionMode=%s", wire.PermissionMode)
	}
	if len(wire.Params.Messages) != 1 || wire.Params.Messages[0].Role != "user" {
		t.Fatalf("messages=%+v", wire.Params.Messages)
	}
	if wire.Params.Messages[0].Content[0].Text != "hi" {
		t.Fatalf("text=%s", wire.Params.Messages[0].Content[0].Text)
	}
}

func TestEmptySystemPlaceholder(t *testing.T) {
	req := Request{Messages: []Message{{Role: "user", Text: "hi"}}}
	wire := req.ToWire(".", "go")
	if wire.Params.System != " " {
		t.Fatalf("expected space placeholder, got %q", wire.Params.System)
	}
}

func TestOpenAIToolsAndImages(t *testing.T) {
	raw := []byte(`{
		"model": "xiaomi/mimo-v2.5",
		"messages": [
			{"role": "user", "content": [
				{"type": "text", "text": "look"},
				{"type": "image_url", "image_url": {"url": "data:image/png;base64,abc"}}
			]},
			{"role": "assistant", "content": null, "reasoning_content": "think", "tool_calls": [
				{"id": "call_1", "type": "function", "function": {"name": "get_weather", "arguments": "{\"city\":\"NYC\"}"}}
			]},
			{"role": "tool", "tool_call_id": "call_1", "content": "72F"}
		],
		"tools": [{"type": "function", "function": {"name": "get_weather", "description": "weather", "parameters": {"type": "object"}}}],
		"tool_choice": "required",
		"reasoning_effort": "high"
	}`)
	req, err := FromOpenAI(raw)
	if err != nil {
		t.Fatal(err)
	}
	if req.ReasoningEffort != "high" {
		t.Fatalf("effort=%s", req.ReasoningEffort)
	}
	if len(req.Messages) != 3 {
		t.Fatalf("n=%d", len(req.Messages))
	}
	if len(req.Messages[0].Images) != 1 {
		t.Fatalf("images=%v", req.Messages[0].Images)
	}
	if req.Messages[1].Reasoning != "think" || req.Messages[1].ToolCalls[0].Name != "get_weather" {
		t.Fatalf("assistant=%+v", req.Messages[1])
	}
	wire := req.ToWire(".", "go")
	choice, ok := wire.Params.ToolChoice.(map[string]any)
	if !ok {
		t.Fatalf("tool_choice=%T %+v", wire.Params.ToolChoice, wire.Params.ToolChoice)
	}
	if choice["type"] != "any" {
		t.Fatalf("tool_choice=%v", wire.Params.ToolChoice)
	}
}

func TestFromAnthropic(t *testing.T) {
	raw := []byte(`{
		"model": "claude-sonnet-4-6",
		"max_tokens": 200,
		"system": "sys",
		"thinking": {"type": "enabled", "budget_tokens": 12000},
		"messages": [
			{"role": "user", "content": "hello"},
			{"role": "assistant", "content": [
				{"type": "thinking", "thinking": "hmm"},
				{"type": "text", "text": "ok"},
				{"type": "tool_use", "id": "toolu_1", "name": "echo", "input": {"x": 1}}
			]},
			{"role": "user", "content": [
				{"type": "tool_result", "tool_use_id": "toolu_1", "content": "pong"},
				{"type": "text", "text": "continue"}
			]}
		],
		"tools": [{"name": "echo", "description": "echo", "input_schema": {"type": "object"}}],
		"tool_choice": {"type": "auto"}
	}`)
	req, err := FromAnthropic(raw)
	if err != nil {
		t.Fatal(err)
	}
	if req.System != "sys" || req.ReasoningEffort != "high" {
		t.Fatalf("system=%q effort=%s", req.System, req.ReasoningEffort)
	}
	if len(req.Messages) != 4 {
		t.Fatalf("messages=%d %+v", len(req.Messages), req.Messages)
	}
	if req.Messages[1].Reasoning != "hmm" {
		t.Fatalf("reasoning=%q", req.Messages[1].Reasoning)
	}
	if req.Messages[2].Role != "tool" || req.Messages[2].ToolResult != "pong" {
		t.Fatalf("tool=%+v", req.Messages[2])
	}
	if req.Messages[3].Text != "continue" {
		t.Fatalf("user=%+v", req.Messages[3])
	}
}
