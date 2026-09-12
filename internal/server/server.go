package server

import (
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"regexp"
	"strings"
	"time"

	"command2api/internal/cc"
	"command2api/internal/config"
	"command2api/internal/convert"
	"command2api/internal/id"
	"command2api/internal/models"
)

var userKey = regexp.MustCompile(`user_[a-zA-Z0-9_-]+`)

type Server struct {
	cfg    config.Config
	client *cc.Client
	http   *http.Server
}

func New(cfg config.Config) *Server {
	s := &Server{cfg: cfg, client: cc.New(cfg)}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /health", s.handleHealth)
	mux.HandleFunc("GET /", s.handleHealth)
	mux.HandleFunc("GET /v1/models", s.handleModels)
	mux.HandleFunc("POST /v1/chat/completions", s.handleChat)
	mux.HandleFunc("POST /v1/messages", s.handleMessages)
	s.http = &http.Server{
		Addr:              fmt.Sprintf("%s:%d", cfg.Host, cfg.Port),
		Handler:           withAccessLog(limitBody(cfg.MaxBodyMB, mux)),
		ReadHeaderTimeout: 15 * time.Second,
	}
	return s
}

func (s *Server) ListenAndServe() error {
	ln, err := net.Listen("tcp", s.http.Addr)
	if err != nil {
		return err
	}
	log.Printf("command2api %s listening on http://%s (upstream %s)", config.Version, ln.Addr(), s.cfg.APIBase)
	return s.http.Serve(ln)
}

func (s *Server) handleHealth(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/" && r.URL.Path != "/health" {
		writeJSON(w, http.StatusNotFound, map[string]any{"error": map[string]any{"message": "not found", "type": "not_found"}})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"status":  "ok",
		"name":    "command2api",
		"version": config.Version,
	})
}

func (s *Server) handleModels(w http.ResponseWriter, r *http.Request) {
	key := s.apiKey(r)
	list := models.Fallback()
	if key != "" {
		list = s.client.Models(r.Context(), key)
	}
	writeJSON(w, http.StatusOK, models.OpenAIList(list))
}

func (s *Server) handleChat(w http.ResponseWriter, r *http.Request) {
	body, err := io.ReadAll(r.Body)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, openaiErr("invalid_request_error", "failed to read body"))
		return
	}
	req, err := convert.FromOpenAI(body)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, openaiErr("invalid_request_error", err.Error()))
		return
	}
	key := s.apiKey(r)
	if key == "" {
		writeJSON(w, http.StatusUnauthorized, openaiErr("authentication_error", "missing API key (Authorization: Bearer user_... or x-api-key)"))
		return
	}
	s.proxy(w, r, key, req, "openai")
}

func (s *Server) handleMessages(w http.ResponseWriter, r *http.Request) {
	body, err := io.ReadAll(r.Body)
	if err != nil {
		writeAnthropicError(w, http.StatusBadRequest, "invalid_request_error", "failed to read body")
		return
	}
	req, err := convert.FromAnthropic(body)
	if err != nil {
		writeAnthropicError(w, http.StatusBadRequest, "invalid_request_error", err.Error())
		return
	}
	key := s.apiKey(r)
	if key == "" {
		writeAnthropicError(w, http.StatusUnauthorized, "authentication_error", "missing API key")
		return
	}
	s.proxy(w, r, key, req, "anthropic")
}

func (s *Server) proxy(w http.ResponseWriter, r *http.Request, key string, req convert.Request, proto string) {
	start := time.Now()
	ctx := r.Context()
	hint := firstNonEmpty(r.Header.Get("x-session-id"), r.Header.Get("x-claude-code-session-id"), req.PromptCacheKey)
	log.Printf("generate start proto=%s model=%s stream=%v key=%s", proto, req.Model, req.Stream, maskKey(key))
	resp, err := s.client.Generate(ctx, key, req, hint)
	if err != nil {
		log.Printf("generate error proto=%s model=%s dur=%s err=%v", proto, req.Model, time.Since(start).Truncate(time.Millisecond), err)
		if proto == "anthropic" {
			writeAnthropicError(w, http.StatusBadGateway, "api_error", err.Error())
		} else {
			writeJSON(w, http.StatusBadGateway, openaiErr("api_error", err.Error()))
		}
		return
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 400 {
		raw, _ := io.ReadAll(io.LimitReader(resp.Body, 8<<10))
		status, typ, msg := mapUpstream(resp.StatusCode, raw)
		log.Printf("generate upstream proto=%s model=%s status=%d mapped=%d dur=%s msg=%s", proto, req.Model, resp.StatusCode, status, time.Since(start).Truncate(time.Millisecond), truncate(msg, 160))
		if proto == "anthropic" {
			writeAnthropicError(w, status, typ, msg)
		} else {
			writeJSON(w, status, openaiErr(typ, msg))
		}
		return
	}

	if req.Stream {
		if proto == "anthropic" {
			streamAnthropic(w, r, resp.Body, req.Model)
		} else {
			streamOpenAI(w, r, resp.Body, req.Model)
		}
		log.Printf("generate done proto=%s model=%s stream=true upstream=%d dur=%s", proto, req.Model, resp.StatusCode, time.Since(start).Truncate(time.Millisecond))
		return
	}
	acc := accumulate(resp.Body)
	if acc.errMsg != "" {
		status, typ, msg := mapUpstream(http.StatusBadGateway, []byte(acc.errMsg))
		log.Printf("generate stream-error proto=%s model=%s dur=%s msg=%s", proto, req.Model, time.Since(start).Truncate(time.Millisecond), truncate(msg, 160))
		if proto == "anthropic" {
			writeAnthropicError(w, status, typ, msg)
		} else {
			writeJSON(w, status, openaiErr(typ, msg))
		}
		return
	}
	log.Printf("generate done proto=%s model=%s stream=false finish=%s dur=%s", proto, req.Model, acc.finish, time.Since(start).Truncate(time.Millisecond))
	if proto == "anthropic" {
		writeJSON(w, http.StatusOK, anthropicMessage(req.Model, acc))
		return
	}
	writeJSON(w, http.StatusOK, openaiMessage(req.Model, acc))
}

type accumulator struct {
	text      string
	reasoning string
	toolCalls []openaiToolCall
	finish    string
	usage     *cc.Usage
	errMsg    string
}

type openaiToolCall struct {
	ID       string `json:"id"`
	Type     string `json:"type"`
	Function struct {
		Name      string `json:"name"`
		Arguments string `json:"arguments"`
	} `json:"function"`
}

func accumulate(r io.Reader) accumulator {
	var acc accumulator
	_ = cc.ReadEvents(r, func(ev cc.Event) error {
		switch ev.Type {
		case "text-delta":
			acc.text += ev.TextDelta()
		case "reasoning-delta":
			acc.reasoning += ev.TextDelta()
		case "tool-call":
			acc.toolCalls = append(acc.toolCalls, toToolCall(ev))
		case "finish", "finish-step":
			if ev.FinishReason != "" {
				acc.finish = cc.MapFinishReason(ev.FinishReason)
			}
			if u := ev.UsageOrTotal(); u != nil {
				acc.usage = u
			}
		case "error":
			acc.errMsg = eventError(ev)
		}
		return nil
	})
	if acc.finish == "" {
		acc.finish = "stop"
	}
	return acc
}

func streamOpenAI(w http.ResponseWriter, r *http.Request, body io.Reader, model string) {
	flusher, _ := w.(http.Flusher)
	reqID := "chatcmpl-" + id.Short(12)
	created := time.Now().Unix()
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.Header().Set("X-Accel-Buffering", "no")
	w.WriteHeader(http.StatusOK)

	write := func(v any) {
		b, _ := json.Marshal(v)
		_, _ = fmt.Fprintf(w, "data: %s\n\n", b)
		if flusher != nil {
			flusher.Flush()
		}
	}

	first := true
	toolIdx := 0
	_ = cc.ReadEvents(body, func(ev cc.Event) error {
		select {
		case <-r.Context().Done():
			return r.Context().Err()
		default:
		}
		switch ev.Type {
		case "text-delta":
			delta := map[string]any{"content": ev.TextDelta()}
			if first {
				delta["role"] = "assistant"
				first = false
			}
			write(openaiChunk(reqID, created, model, delta, nil, nil))
		case "reasoning-delta":
			delta := map[string]any{"reasoning_content": ev.TextDelta()}
			if first {
				delta["role"] = "assistant"
				first = false
			}
			write(openaiChunk(reqID, created, model, delta, nil, nil))
		case "tool-call":
			tc := toToolCall(ev)
			entry := map[string]any{
				"index": toolIdx,
				"id":    tc.ID,
				"type":  "function",
				"function": map[string]any{
					"name":      tc.Function.Name,
					"arguments": tc.Function.Arguments,
				},
			}
			toolIdx++
			delta := map[string]any{"tool_calls": []any{entry}}
			if first {
				delta["role"] = "assistant"
				delta["content"] = nil
				first = false
			}
			write(openaiChunk(reqID, created, model, delta, nil, nil))
		case "finish":
			u := openaiUsage(ev.UsageOrTotal())
			write(openaiChunk(reqID, created, model, map[string]any{}, cc.MapFinishReason(ev.FinishReason), u))
		case "error":
			write(openaiErr("api_error", eventError(ev)))
		}
		return nil
	})
	_, _ = io.WriteString(w, "data: [DONE]\n\n")
	if flusher != nil {
		flusher.Flush()
	}
}

func streamAnthropic(w http.ResponseWriter, r *http.Request, body io.Reader, model string) {
	flusher, _ := w.(http.Flusher)
	msgID := "msg_" + id.Short(12)
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.Header().Set("X-Accel-Buffering", "no")
	w.WriteHeader(http.StatusOK)

	emit := func(event string, v any) {
		b, _ := json.Marshal(v)
		_, _ = fmt.Fprintf(w, "event: %s\ndata: %s\n\n", event, b)
		if flusher != nil {
			flusher.Flush()
		}
	}
	emit("message_start", map[string]any{
		"type": "message_start",
		"message": map[string]any{
			"id": msgID, "type": "message", "role": "assistant", "content": []any{},
			"model": model, "usage": map[string]any{"input_tokens": 0, "output_tokens": 0},
		},
	})

	idx := -1
	cur := ""
	thinking := ""
	closeBlock := func() {
		if idx < 0 {
			return
		}
		if cur == "thinking" {
			emit("content_block_delta", map[string]any{
				"type": "content_block_delta", "index": idx,
				"delta": map[string]any{"type": "signature_delta", "signature": convert.FakeThinkingSignature(thinking)},
			})
			thinking = ""
		}
		emit("content_block_stop", map[string]any{"type": "content_block_stop", "index": idx})
		idx = -1
		cur = ""
	}
	open := func(kind string, block any) {
		if cur != kind {
			closeBlock()
			idx++
			cur = kind
			emit("content_block_start", map[string]any{
				"type": "content_block_start", "index": idx, "content_block": block,
			})
		}
	}

	stop := "end_turn"
	var usage *cc.Usage
	_ = cc.ReadEvents(body, func(ev cc.Event) error {
		select {
		case <-r.Context().Done():
			return r.Context().Err()
		default:
		}
		switch ev.Type {
		case "reasoning-delta":
			open("thinking", map[string]any{"type": "thinking", "thinking": ""})
			thinking += ev.TextDelta()
			emit("content_block_delta", map[string]any{
				"type": "content_block_delta", "index": idx,
				"delta": map[string]any{"type": "thinking_delta", "thinking": ev.TextDelta()},
			})
		case "text-delta":
			open("text", map[string]any{"type": "text", "text": ""})
			emit("content_block_delta", map[string]any{
				"type": "content_block_delta", "index": idx,
				"delta": map[string]any{"type": "text_delta", "text": ev.TextDelta()},
			})
		case "tool-call":
			closeBlock()
			tc := toToolCall(ev)
			i := idx + 1
			if i < 0 {
				i = 0
			}
			emit("content_block_start", map[string]any{
				"type": "content_block_start", "index": i,
				"content_block": map[string]any{"type": "tool_use", "id": tc.ID, "name": tc.Function.Name, "input": map[string]any{}},
			})
			emit("content_block_delta", map[string]any{
				"type": "content_block_delta", "index": i,
				"delta": map[string]any{"type": "input_json_delta", "partial_json": tc.Function.Arguments},
			})
			emit("content_block_stop", map[string]any{"type": "content_block_stop", "index": i})
			idx = i
			cur = ""
		case "finish", "finish-step":
			if ev.FinishReason != "" {
				stop = convert.AnthropicStopReason(ev.FinishReason)
			}
			if u := ev.UsageOrTotal(); u != nil {
				usage = u
			}
		case "error":
			emit("error", map[string]any{"type": "error", "error": map[string]any{"type": "api_error", "message": eventError(ev)}})
		}
		return nil
	})
	closeBlock()
	inTok, outTok, cacheRead, cacheWrite := 0, 0, 0, 0
	if usage != nil {
		inTok = usage.NoCache()
		outTok = usage.OutputTokens
		cacheRead = usage.CacheRead()
		cacheWrite = usage.CacheWrite()
	}
	emit("message_delta", map[string]any{
		"type":  "message_delta",
		"delta": map[string]any{"stop_reason": stop},
		"usage": map[string]any{
			"input_tokens":                inTok,
			"output_tokens":               outTok,
			"cache_read_input_tokens":     cacheRead,
			"cache_creation_input_tokens": cacheWrite,
		},
	})
	emit("message_stop", map[string]any{"type": "message_stop"})
}

func openaiMessage(model string, acc accumulator) map[string]any {
	msg := map[string]any{"role": "assistant", "content": nilString(acc.text)}
	if acc.reasoning != "" {
		msg["reasoning_content"] = acc.reasoning
	}
	if len(acc.toolCalls) > 0 {
		msg["tool_calls"] = acc.toolCalls
	}
	return map[string]any{
		"id":      "chatcmpl-" + id.Short(12),
		"object":  "chat.completion",
		"created": time.Now().Unix(),
		"model":   model,
		"choices": []any{map[string]any{
			"index": 0, "message": msg, "finish_reason": acc.finish,
		}},
		"usage": openaiUsage(acc.usage),
	}
}

func anthropicMessage(model string, acc accumulator) map[string]any {
	content := []any{}
	if acc.reasoning != "" {
		content = append(content, map[string]any{
			"type": "thinking", "thinking": acc.reasoning, "signature": convert.FakeThinkingSignature(acc.reasoning),
		})
	}
	if acc.text != "" {
		content = append(content, map[string]any{"type": "text", "text": acc.text})
	}
	for _, tc := range acc.toolCalls {
		var input any = map[string]any{}
		_ = json.Unmarshal([]byte(tc.Function.Arguments), &input)
		content = append(content, map[string]any{
			"type": "tool_use", "id": tc.ID, "name": tc.Function.Name, "input": input,
		})
	}
	inTok, outTok, cacheRead, cacheWrite := 0, 0, 0, 0
	if acc.usage != nil {
		inTok = acc.usage.NoCache()
		outTok = acc.usage.OutputTokens
		cacheRead = acc.usage.CacheRead()
		cacheWrite = acc.usage.CacheWrite()
	}
	return map[string]any{
		"id":            "msg_" + id.Short(12),
		"type":          "message",
		"role":          "assistant",
		"model":         model,
		"content":       content,
		"stop_reason":   convert.AnthropicStopReason(acc.finish),
		"stop_sequence": nil,
		"usage": map[string]any{
			"input_tokens":                inTok,
			"output_tokens":               outTok,
			"cache_read_input_tokens":     cacheRead,
			"cache_creation_input_tokens": cacheWrite,
		},
	}
}

func openaiChunk(id string, created int64, model string, delta any, finish any, usage any) map[string]any {
	chunk := map[string]any{
		"id": id, "object": "chat.completion.chunk", "created": created, "model": model,
		"choices": []any{map[string]any{"index": 0, "delta": delta, "finish_reason": finish}},
	}
	if usage != nil {
		chunk["usage"] = usage
	}
	return chunk
}

func openaiUsage(u *cc.Usage) map[string]any {
	if u == nil {
		return map[string]any{
			"prompt_tokens": 0, "completion_tokens": 0, "total_tokens": 0,
			"prompt_tokens_details": map[string]any{"cached_tokens": 0},
		}
	}
	return map[string]any{
		"prompt_tokens":     u.InputTokens,
		"completion_tokens": u.OutputTokens,
		"total_tokens":      u.InputTokens + u.OutputTokens,
		"prompt_tokens_details": map[string]any{
			"cached_tokens": u.CacheRead(),
		},
	}
}

func toToolCall(ev cc.Event) openaiToolCall {
	callID := ev.ToolCallID
	if callID == "" {
		callID = "call_" + id.Short(8)
	}
	args := "{}"
	if len(ev.Input) > 0 {
		if ev.Input[0] == '"' {
			var s string
			if json.Unmarshal(ev.Input, &s) == nil {
				args = s
			} else {
				args = string(ev.Input)
			}
		} else {
			args = string(ev.Input)
		}
	}
	tc := openaiToolCall{ID: callID, Type: "function"}
	tc.Function.Name = ev.ToolName
	tc.Function.Arguments = args
	return tc
}

func eventError(ev cc.Event) string {
	if ev.Error != nil && ev.Error.Message != "" {
		return ev.Error.Message
	}
	if ev.Message != "" {
		return ev.Message
	}
	return "upstream error"
}

func mapUpstream(status int, body []byte) (int, string, string) {
	msg := strings.TrimSpace(string(body))
	if msg == "" {
		msg = fmt.Sprintf("CC API error (%d)", status)
	} else {
		var parsed map[string]any
		if json.Unmarshal(body, &parsed) == nil {
			if e, ok := parsed["error"].(map[string]any); ok {
				if m, ok := e["message"].(string); ok {
					msg = m
				}
			} else if m, ok := parsed["message"].(string); ok {
				msg = m
			}
		}
		if len(msg) > 400 {
			msg = msg[:400]
		}
	}
	switch status {
	case 400, 422:
		return 400, "invalid_request_error", msg
	case 401, 403:
		return 401, "authentication_error", msg
	case 402, 429:
		return 429, "rate_limit_error", msg
	case 404:
		return 404, "not_found", msg
	default:
		return 502, "api_error", msg
	}
}

func (s *Server) apiKey(r *http.Request) string {
	if k := extractKey(r.Header.Get("Authorization")); k != "" {
		return k
	}
	if k := extractKey(r.Header.Get("x-api-key")); k != "" {
		return k
	}
	return extractKey(s.cfg.APIKey)
}

func extractKey(v string) string {
	v = strings.TrimSpace(v)
	if v == "" {
		return ""
	}
	v = strings.TrimPrefix(v, "Bearer ")
	if m := userKey.FindString(v); m != "" {
		return m
	}
	if strings.HasPrefix(v, "user_") {
		return v
	}
	return ""
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func writeAnthropicError(w http.ResponseWriter, status int, typ, msg string) {
	writeJSON(w, status, map[string]any{
		"type":  "error",
		"error": map[string]any{"type": typ, "message": msg},
	})
}

func openaiErr(typ, msg string) map[string]any {
	return map[string]any{"error": map[string]any{"message": msg, "type": typ}}
}

func withAccessLog(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/health" || r.URL.Path == "/" {
			next.ServeHTTP(w, r)
			return
		}
		lw := &logResponseWriter{ResponseWriter: w, status: http.StatusOK}
		start := time.Now()
		next.ServeHTTP(lw, r)
		log.Printf("%s %s %d %dB %s", r.Method, r.URL.Path, lw.status, lw.bytes, time.Since(start).Truncate(time.Millisecond))
	})
}

type logResponseWriter struct {
	http.ResponseWriter
	status int
	bytes  int
}

func (w *logResponseWriter) WriteHeader(code int) {
	w.status = code
	w.ResponseWriter.WriteHeader(code)
}

func (w *logResponseWriter) Write(b []byte) (int, error) {
	n, err := w.ResponseWriter.Write(b)
	w.bytes += n
	return n, err
}

func (w *logResponseWriter) Flush() {
	if f, ok := w.ResponseWriter.(http.Flusher); ok {
		f.Flush()
	}
}

func (w *logResponseWriter) Unwrap() http.ResponseWriter {
	return w.ResponseWriter
}

func maskKey(k string) string {
	if len(k) <= 12 {
		return "user_***"
	}
	return k[:8] + "…" + k[len(k)-4:]
}

func truncate(s string, n int) string {
	s = strings.ReplaceAll(strings.TrimSpace(s), "\n", " ")
	if len(s) <= n {
		return s
	}
	return s[:n] + "…"
}

func limitBody(mb int, next http.Handler) http.Handler {
	if mb <= 0 {
		mb = 32
	}
	max := int64(mb) << 20
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Body != nil {
			r.Body = http.MaxBytesReader(w, r.Body, max)
		}
		next.ServeHTTP(w, r)
	})
}

func nilString(s string) any {
	if s == "" {
		return nil
	}
	return s
}

func firstNonEmpty(vs ...string) string {
	for _, v := range vs {
		if strings.TrimSpace(v) != "" {
			return v
		}
	}
	return ""
}
