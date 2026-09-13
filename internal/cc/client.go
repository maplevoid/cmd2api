package cc

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"
	"time"

	"cmd2api/internal/config"
	"cmd2api/internal/convert"
	"cmd2api/internal/models"
)

type Client struct {
	cfg       config.Config
	http      *http.Client
	state     *StateStore
	mu        sync.Mutex
	models    []models.Model
	fetchedAt time.Time
}

func New(cfg config.Config) *Client {
	return &Client{
		cfg:   cfg,
		http:  &http.Client{Timeout: 0},
		state: NewStateStore(),
	}
}

func (c *Client) Generate(ctx context.Context, apiKey string, req convert.Request, sessionHint string) (*http.Response, error) {
	_ = c.ensureInit(ctx, apiKey)
	body := req.ToWire(c.cfg.ProjectSlug, Environment())
	raw, err := json.Marshal(body)
	if err != nil {
		return nil, err
	}
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, c.cfg.APIBase+"/alpha/generate", bytes.NewReader(raw))
	if err != nil {
		return nil, err
	}
	c.applyHeaders(httpReq, apiKey, sessionHint, req.PromptCacheKey)
	httpReq.Header.Set("Content-Type", "application/json")
	return c.http.Do(httpReq)
}

func (c *Client) Models(ctx context.Context, apiKey string) []models.Model {
	c.mu.Lock()
	if time.Since(c.fetchedAt) < 5*time.Minute && len(c.models) > 0 {
		out := c.models
		c.mu.Unlock()
		return out
	}
	c.mu.Unlock()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.cfg.APIBase+"/provider/v1/models", nil)
	if err != nil {
		return models.Fallback()
	}
	c.applyHeaders(req, apiKey, "", "")
	resp, err := c.http.Do(req)
	if err != nil {
		return models.Fallback()
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return models.Fallback()
	}
	var payload struct {
		Data []models.Model `json:"data"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&payload); err != nil || len(payload.Data) == 0 {
		return models.Fallback()
	}
	for i := range payload.Data {
		if payload.Data[i].Object == "" {
			payload.Data[i].Object = "model"
		}
		if payload.Data[i].OwnedBy == "" {
			payload.Data[i].OwnedBy = "commandcode"
		}
	}
	c.mu.Lock()
	c.models = payload.Data
	c.fetchedAt = time.Now()
	c.mu.Unlock()
	return payload.Data
}

func (c *Client) ensureInit(ctx context.Context, apiKey string) error {
	fp, need := c.state.NeedInit(apiKey)
	if !need {
		return nil
	}
	var wg sync.WaitGroup
	wg.Add(2)
	go func() {
		defer wg.Done()
		_ = c.postJSON(ctx, apiKey, "/alpha/fingerprint/record", fp)
	}()
	go func() {
		defer wg.Done()
		_ = c.postJSON(ctx, apiKey, "/alpha/lifecycle-events", map[string]any{
			"eventType": "cli_session_exists",
			"metadata": map[string]any{
				"sessionId":  compactSession(c.state.Session(apiKey, "")),
				"cliVersion": c.cfg.CLIVersion,
				"mode":       "interactive",
				"os":         fp.Components.Platform + "-" + fp.Components.Arch,
			},
		})
	}()
	wg.Wait()
	c.state.MarkInit(apiKey)
	return nil
}

func (c *Client) postJSON(ctx context.Context, apiKey, path string, body any) error {
	raw, err := json.Marshal(body)
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.cfg.APIBase+path, bytes.NewReader(raw))
	if err != nil {
		return err
	}
	c.applyHeaders(req, apiKey, "", "")
	req.Header.Set("Content-Type", "application/json")
	resp, err := c.http.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	_, _ = io.Copy(io.Discard, resp.Body)
	if resp.StatusCode >= 400 {
		return fmt.Errorf("%s: %s", path, resp.Status)
	}
	return nil
}

func (c *Client) applyHeaders(req *http.Request, apiKey, sessionHint, promptCacheKey string) {
	session := sessionHint
	if session == "" {
		session = promptCacheKey
	}
	req.Header.Set("Authorization", "Bearer "+apiKey)
	req.Header.Set("User-Agent", "cli")
	req.Header.Set("x-cli-environment", "production")
	req.Header.Set("x-command-code-version", c.cfg.CLIVersion)
	req.Header.Set("x-session-id", c.state.Session(apiKey, session))
	req.Header.Set("x-taste-learning", "false")
	req.Header.Set("x-project-slug", slug(c.cfg.ProjectSlug, c.state.Session(apiKey, session)))
	req.Header.Set("x-co-flag", "false")
	if c.cfg.ZDR {
		req.Header.Set("x-cmd-zdr", "1")
	}
}

func slug(project, session string) string {
	if project != "" {
		return project
	}
	if len(session) >= 8 {
		return "cmd2api-" + session[:8]
	}
	return "cmd2api"
}

func compactSession(id string) string {
	s := strings.ReplaceAll(id, "-", "")
	if len(s) > 16 {
		s = s[:16]
	}
	return "sess_" + s
}
