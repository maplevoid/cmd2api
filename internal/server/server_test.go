package server

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"cmd2api/internal/cc"
	"cmd2api/internal/config"
)

func TestHealthAndAuth(t *testing.T) {
	s := New(config.Config{Host: "127.0.0.1", Port: 0, APIBase: "http://127.0.0.1:1", MaxBodyMB: 1, CLIVersion: "1.53.1"})
	rr := httptest.NewRecorder()
	s.http.Handler.ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/health", nil))
	if rr.Code != 200 {
		t.Fatalf("health=%d", rr.Code)
	}

	rr = httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(`{"model":"x","messages":[{"role":"user","content":"hi"}]}`))
	req.Header.Set("Content-Type", "application/json")
	s.http.Handler.ServeHTTP(rr, req)
	if rr.Code != 401 {
		t.Fatalf("unauth=%d body=%s", rr.Code, rr.Body.String())
	}
}

func TestExtractKey(t *testing.T) {
	if got := extractKey("Bearer token_user_abc123"); got != "user_abc123" {
		t.Fatalf("got %q", got)
	}
	if got := extractKey("user_zzz"); got != "user_zzz" {
		t.Fatalf("got %q", got)
	}
}

func TestOpenAIRoundTrip(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/alpha/fingerprint/record", "/alpha/lifecycle-events":
			w.WriteHeader(204)
			return
		case "/provider/v1/models":
			_ = json.NewEncoder(w).Encode(map[string]any{"data": []any{map[string]any{"id": "deepseek/deepseek-v4-flash"}}})
			return
		case "/alpha/generate":
			if r.Header.Get("Authorization") != "Bearer user_testkey" {
				t.Errorf("auth=%s", r.Header.Get("Authorization"))
			}
			if r.Header.Get("x-command-code-version") == "" {
				t.Error("missing version header")
			}
			w.Header().Set("Content-Type", "application/x-ndjson")
			_, _ = io.WriteString(w, `{"type":"text-delta","text":"hello"}`+"\n")
			_, _ = io.WriteString(w, `{"type":"finish","finishReason":"stop","totalUsage":{"inputTokens":10,"outputTokens":2,"cachedInputTokens":4}}`+"\n")
			return
		default:
			http.NotFound(w, r)
		}
	}))
	defer upstream.Close()

	s := New(config.Config{
		Host: "127.0.0.1", Port: 0, APIBase: upstream.URL, MaxBodyMB: 1, CLIVersion: "1.53.1", ProjectSlug: "cmd2api",
	})

	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(`{"model":"deepseek/deepseek-v4-flash","messages":[{"role":"user","content":"hi"}]}`))
	req.Header.Set("Authorization", "Bearer user_testkey")
	req.Header.Set("Content-Type", "application/json")
	s.http.Handler.ServeHTTP(rr, req)
	if rr.Code != 200 {
		t.Fatalf("status=%d body=%s", rr.Code, rr.Body.String())
	}
	var out map[string]any
	if err := json.Unmarshal(rr.Body.Bytes(), &out); err != nil {
		t.Fatal(err)
	}
	choice := out["choices"].([]any)[0].(map[string]any)
	msg := choice["message"].(map[string]any)
	if msg["content"] != "hello" {
		t.Fatalf("content=%v", msg["content"])
	}

	rr = httptest.NewRecorder()
	req = httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(`{"model":"deepseek/deepseek-v4-flash","stream":true,"messages":[{"role":"user","content":"hi"}]}`))
	req.Header.Set("Authorization", "Bearer user_testkey")
	s.http.Handler.ServeHTTP(rr, req)
	if rr.Code != 200 {
		t.Fatalf("stream status=%d body=%s", rr.Code, rr.Body.String())
	}
	body := rr.Body.String()
	if !strings.Contains(body, `"content":"hello"`) || !strings.Contains(body, "data: [DONE]") {
		t.Fatalf("sse=%s", body)
	}

	rr = httptest.NewRecorder()
	req = httptest.NewRequest(http.MethodPost, "/v1/messages", strings.NewReader(`{"model":"claude-sonnet-4-6","max_tokens":32,"messages":[{"role":"user","content":"hi"}]}`))
	req.Header.Set("x-api-key", "user_testkey")
	s.http.Handler.ServeHTTP(rr, req)
	if rr.Code != 200 {
		t.Fatalf("anthropic status=%d body=%s", rr.Code, rr.Body.String())
	}
	if err := json.Unmarshal(rr.Body.Bytes(), &out); err != nil {
		t.Fatal(err)
	}
	content := out["content"].([]any)[0].(map[string]any)
	if content["text"] != "hello" {
		t.Fatalf("anthropic=%v", out)
	}
}

func TestReadEvents(t *testing.T) {
	r := strings.NewReader(`{"type":"text-delta","text":"a"}` + "\n" + `not-json` + "\n" + `{"type":"finish","finishReason":"tool-calls"}` + "\n")
	var types []string
	if err := cc.ReadEvents(r, func(ev cc.Event) error {
		types = append(types, ev.Type)
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if strings.Join(types, ",") != "text-delta,finish" {
		t.Fatalf("%v", types)
	}
	if cc.MapFinishReason("tool-calls") != "tool_calls" {
		t.Fatal(cc.MapFinishReason("tool-calls"))
	}
}
