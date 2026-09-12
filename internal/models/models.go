package models

import "time"

type Model struct {
	ID      string `json:"id"`
	Object  string `json:"object"`
	Created int64  `json:"created"`
	OwnedBy string `json:"owned_by"`
}

var fallback = []string{
	"deepseek/deepseek-v4-flash",
	"deepseek/deepseek-v4-pro",
	"deepseek/deepseek-v4.1-flash",
	"moonshotai/Kimi-K2.7-Code",
	"moonshotai/Kimi-K2.6",
	"zai-org/GLM-5.3",
	"z-ai/glm-5.3-flash",
	"MiniMaxAI/MiniMax-M3",
	"xiaomi/mimo-v2.5",
	"Qwen/Qwen3.8-Flash",
	"Qwen/Qwen3.7-Flash",
	"claude-sonnet-4-6",
	"claude-haiku-4-5-20251001",
	"gpt-5.6-luna",
	"gpt-5.4-mini",
}

func Fallback() []Model {
	created := time.Now().Unix()
	out := make([]Model, 0, len(fallback))
	for _, id := range fallback {
		out = append(out, Model{ID: id, Object: "model", Created: created, OwnedBy: "commandcode"})
	}
	return out
}

func OpenAIList(ms []Model) map[string]any {
	if ms == nil {
		ms = Fallback()
	}
	return map[string]any{"object": "list", "data": ms}
}
