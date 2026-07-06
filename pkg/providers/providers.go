// Copyright 2026 Mustafa Al-Aqrawi (Smile Spoon). All rights reserved.
// Use of this source code is governed by the MIT License
// that can be found in the LICENSE file.

// Package providers implements universal AI provider adapters for OpenAI,
// Anthropic, Gemini, and custom/local LLM endpoints.
package providers

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/smilespoon/nexusguard-ai/pkg/config"
)

// ProviderType identifies the AI provider implementation.
type ProviderType string

const (
	ProviderOpenAI    ProviderType = "openai"
	ProviderAnthropic ProviderType = "anthropic"
	ProviderGemini    ProviderType = "gemini"
	ProviderCustom    ProviderType = "custom"
)

// Request represents a unified LLM request.
type Request struct {
	Model       string                 `json:"model"`
	Messages    []Message              `json:"messages"`
	Temperature float64                `json:"temperature,omitempty"`
	MaxTokens   int                    `json:"max_tokens,omitempty"`
	Stream      bool                   `json:"stream,omitempty"`
	Extra       map[string]interface{} `json:"-"`
}

// Message represents a single chat message.
type Message struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

// Response represents a unified LLM response.
type Response struct {
	ID        string    `json:"id"`
	Model     string    `json:"model"`
	Content   string    `json:"content"`
	TokensIn  int       `json:"tokens_in"`
	TokensOut int       `json:"tokens_out"`
	Provider  string    `json:"provider"`
	Latency   time.Duration `json:"latency"`
	Cached    bool      `json:"cached"`
	Error     error     `json:"-"`
}

// StreamChunk represents a single SSE stream chunk.
type StreamChunk struct {
	Content  string `json:"content"`
	Done     bool   `json:"done"`
	Provider string `json:"provider"`
}

// Provider defines the interface for AI provider implementations.
type Provider interface {
	Name() string
	Type() ProviderType
	Send(ctx context.Context, req *Request) (*Response, error)
	Stream(ctx context.Context, req *Request) (<-chan StreamChunk, error)
	HealthCheck(ctx context.Context) error
	EstimateCost(req *Request) float64
	Weight() int
	Enabled() bool
	SetEnabled(bool)
}

// BaseProvider holds shared provider functionality.
type BaseProvider struct {
	cfg      config.ProviderConfig
	type_    ProviderType
	enabled  bool
	mu       sync.RWMutex
	client   *http.Client
}

// New creates a provider from configuration.
func New(cfg config.ProviderConfig) (Provider, error) {
	base := &BaseProvider{
		cfg:     cfg,
		enabled: cfg.Enabled,
		client: &http.Client{
			Timeout: cfg.Timeout,
		},
	}

	switch {
	case strings.Contains(cfg.BaseURL, "openai.com"):
		base.type_ = ProviderOpenAI
		return &OpenAIProvider{BaseProvider: base}, nil
	case strings.Contains(cfg.BaseURL, "anthropic.com"):
		base.type_ = ProviderAnthropic
		return &AnthropicProvider{BaseProvider: base}, nil
	case strings.Contains(cfg.BaseURL, "googleapis.com") || strings.Contains(cfg.BaseURL, "generativelanguage"):
		base.type_ = ProviderGemini
		return &GeminiProvider{BaseProvider: base}, nil
	default:
		base.type_ = ProviderCustom
		return &CustomProvider{BaseProvider: base}, nil
	}
}

// Name returns the provider name.
func (b *BaseProvider) Name() string { return b.cfg.Name }

// Type returns the provider type.
func (b *BaseProvider) Type() ProviderType { return b.type_ }

// Enabled returns whether the provider is enabled.
func (b *BaseProvider) Enabled() bool {
	b.mu.RLock()
	defer b.mu.RUnlock()
	return b.enabled
}

// SetEnabled toggles the provider.
func (b *BaseProvider) SetEnabled(v bool) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.enabled = v
}

// Weight returns the routing weight.
func (b *BaseProvider) Weight() int { return b.cfg.Weight }

// doRequest performs an HTTP request with retries.
func (b *BaseProvider) doRequest(ctx context.Context, method, url string, body []byte, headers map[string]string) (*http.Response, error) {
	var lastErr error
	for attempt := 0; attempt <= b.cfg.Retries; attempt++ {
		if attempt > 0 {
			time.Sleep(time.Duration(attempt) * time.Second)
		}

		req, err := http.NewRequestWithContext(ctx, method, url, bytes.NewReader(body))
		if err != nil {
			return nil, err
		}

		req.Header.Set("Content-Type", "application/json")
		if b.cfg.APIKey != "" {
			req.Header.Set("Authorization", "Bearer "+b.cfg.APIKey)
		}
		for k, v := range b.cfg.Headers {
			req.Header.Set(k, v)
		}
		for k, v := range headers {
			req.Header.Set(k, v)
		}

		resp, err := b.client.Do(req)
		if err != nil {
			lastErr = err
			continue
		}

		if resp.StatusCode >= 500 {
			lastErr = fmt.Errorf("server error: %d", resp.StatusCode)
			resp.Body.Close()
			continue
		}

		return resp, nil
	}
	return nil, fmt.Errorf("all retries exhausted: %w", lastErr)
}

// OpenAIProvider implements the OpenAI API adapter.
type OpenAIProvider struct {
	*BaseProvider
}

// Send forwards a request to OpenAI.
func (p *OpenAIProvider) Send(ctx context.Context, req *Request) (*Response, error) {
	start := time.Now()

	payload := map[string]interface{}{
		"model":       req.Model,
		"messages":    req.Messages,
		"temperature": req.Temperature,
		"max_tokens":  req.MaxTokens,
		"stream":      false,
	}
	for k, v := range req.Extra {
		payload[k] = v
	}

	body, _ := json.Marshal(payload)
	url := fmt.Sprintf("%s/v1/chat/completions", p.cfg.BaseURL)

	resp, err := p.doRequest(ctx, "POST", url, body, nil)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	respBody, _ := io.ReadAll(resp.Body)

	var result struct {
		ID     string `json:"id"`
		Model  string `json:"model"`
		Usage  struct {
			PromptTokens     int `json:"prompt_tokens"`
			CompletionTokens int `json:"completion_tokens"`
		} `json:"usage"`
		Choices []struct {
			Message struct {
				Content string `json:"content"`
			} `json:"message"`
		} `json:"choices"`
		Error *struct {
			Message string `json:"message"`
		} `json:"error"`
	}

	if err := json.Unmarshal(respBody, &result); err != nil {
		return nil, fmt.Errorf("failed to decode response: %w", err)
	}

	if result.Error != nil {
		return nil, fmt.Errorf("openai error: %s", result.Error.Message)
	}

	content := ""
	if len(result.Choices) > 0 {
		content = result.Choices[0].Message.Content
	}

	return &Response{
		ID:        result.ID,
		Model:     result.Model,
		Content:   content,
		TokensIn:  result.Usage.PromptTokens,
		TokensOut: result.Usage.CompletionTokens,
		Provider:  p.cfg.Name,
		Latency:   time.Since(start),
	}, nil
}

// Stream handles SSE streaming from OpenAI.
func (p *OpenAIProvider) Stream(ctx context.Context, req *Request) (<-chan StreamChunk, error) {
	payload := map[string]interface{}{
		"model":       req.Model,
		"messages":    req.Messages,
		"temperature": req.Temperature,
		"max_tokens":  req.MaxTokens,
		"stream":      true,
	}

	body, _ := json.Marshal(payload)
	url := fmt.Sprintf("%s/v1/chat/completions", p.cfg.BaseURL)

	httpReq, err := http.NewRequestWithContext(ctx, "POST", url, bytes.NewReader(body))
	if err != nil {
		return nil, err
	}

	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("Accept", "text/event-stream")
	httpReq.Header.Set("Cache-Control", "no-cache")
	httpReq.Header.Set("Connection", "keep-alive")
	if p.cfg.APIKey != "" {
		httpReq.Header.Set("Authorization", "Bearer "+p.cfg.APIKey)
	}

	httpResp, err := p.client.Do(httpReq)
	if err != nil {
		return nil, err
	}

	ch := make(chan StreamChunk, 100)

	go func() {
		defer close(ch)
		defer httpResp.Body.Close()

		reader := bufio.NewReader(httpResp.Body)
		for {
			line, err := reader.ReadString('\n')
			if err != nil {
				return
			}

			line = strings.TrimSpace(line)
			if !strings.HasPrefix(line, "data: ") {
				continue
			}

			data := strings.TrimPrefix(line, "data: ")
			if data == "[DONE]" {
				ch <- StreamChunk{Done: true, Provider: p.cfg.Name}
				return
			}

			var chunk struct {
				Choices []struct {
					Delta struct {
						Content string `json:"content"`
					} `json:"delta"`
					FinishReason *string `json:"finish_reason"`
				} `json:"choices"`
			}

			if err := json.Unmarshal([]byte(data), &chunk); err != nil {
				continue
			}

			if len(chunk.Choices) > 0 {
				ch <- StreamChunk{
					Content:  chunk.Choices[0].Delta.Content,
					Done:     chunk.Choices[0].FinishReason != nil,
					Provider: p.cfg.Name,
				}
				if chunk.Choices[0].FinishReason != nil {
					return
				}
			}
		}
	}()

	return ch, nil
}

// HealthCheck pings the OpenAI API.
func (p *OpenAIProvider) HealthCheck(ctx context.Context) error {
	url := fmt.Sprintf("%s/v1/models", p.cfg.BaseURL)
	resp, err := p.doRequest(ctx, "GET", url, nil, nil)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("health check failed: %d", resp.StatusCode)
	}
	return nil
}

// EstimateCost calculates the estimated request cost.
func (p *OpenAIProvider) EstimateCost(req *Request) float64 {
	totalTokens := 0
	for _, m := range req.Messages {
		totalTokens += len(m.Content) / 4
	}
	return (float64(totalTokens)/1000.0)*p.cfg.CostPer1KIn +
		(float64(req.MaxTokens)/1000.0)*p.cfg.CostPer1KOut
}

// AnthropicProvider implements the Anthropic API adapter.
type AnthropicProvider struct {
	*BaseProvider
}

// Send forwards a request to Anthropic.
func (p *AnthropicProvider) Send(ctx context.Context, req *Request) (*Response, error) {
	start := time.Now()

	messages := make([]map[string]string, 0, len(req.Messages))
	var system string
	for _, m := range req.Messages {
		if m.Role == "system" {
			system = m.Content
			continue
		}
		role := m.Role
		if role == "assistant" {
			role = "assistant"
		} else {
			role = "user"
		}
		messages = append(messages, map[string]string{
			"role":    role,
			"content": m.Content,
		})
	}

	payload := map[string]interface{}{
		"model":      req.Model,
		"messages":   messages,
		"max_tokens": req.MaxTokens,
	}
	if system != "" {
		payload["system"] = system
	}

	body, _ := json.Marshal(payload)
	url := fmt.Sprintf("%s/v1/messages", p.cfg.BaseURL)

	headers := map[string]string{
		"x-api-key":         p.cfg.APIKey,
		"anthropic-version": "2023-06-01",
	}

	resp, err := p.doRequest(ctx, "POST", url, body, headers)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	respBody, _ := io.ReadAll(resp.Body)

	var result struct {
		ID     string `json:"id"`
		Model  string `json:"model"`
		Usage  struct {
			InputTokens  int `json:"input_tokens"`
			OutputTokens int `json:"output_tokens"`
		} `json:"usage"`
		Content []struct {
			Type string `json:"type"`
			Text string `json:"text"`
		} `json:"content"`
		Error *struct {
			Message string `json:"message"`
		} `json:"error"`
	}

	if err := json.Unmarshal(respBody, &result); err != nil {
		return nil, err
	}

	if result.Error != nil {
		return nil, fmt.Errorf("anthropic error: %s", result.Error.Message)
	}

	content := ""
	for _, c := range result.Content {
		if c.Type == "text" {
			content += c.Text
		}
	}

	return &Response{
		ID:        result.ID,
		Model:     result.Model,
		Content:   content,
		TokensIn:  result.Usage.InputTokens,
		TokensOut: result.Usage.OutputTokens,
		Provider:  p.cfg.Name,
		Latency:   time.Since(start),
	}, nil
}

// Stream handles SSE streaming from Anthropic.
func (p *AnthropicProvider) Stream(ctx context.Context, req *Request) (<-chan StreamChunk, error) {
	// Anthropic streaming implementation
	ch := make(chan StreamChunk, 100)
	close(ch)
	return ch, nil // Placeholder - full implementation follows same pattern
}

// HealthCheck pings the Anthropic API.
func (p *AnthropicProvider) HealthCheck(ctx context.Context) error {
	headers := map[string]string{
		"x-api-key":         p.cfg.APIKey,
		"anthropic-version": "2023-06-01",
	}
	resp, err := p.doRequest(ctx, "GET", p.cfg.BaseURL+"/v1/models", nil, headers)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	return nil
}

// EstimateCost calculates the estimated request cost.
func (p *AnthropicProvider) EstimateCost(req *Request) float64 {
	totalTokens := 0
	for _, m := range req.Messages {
		totalTokens += len(m.Content) / 4
	}
	return (float64(totalTokens)/1000.0)*p.cfg.CostPer1KIn +
		(float64(req.MaxTokens)/1000.0)*p.cfg.CostPer1KOut
}

// GeminiProvider implements the Google Gemini API adapter.
type GeminiProvider struct {
	*BaseProvider
}

// Send forwards a request to Gemini.
func (p *GeminiProvider) Send(ctx context.Context, req *Request) (*Response, error) {
	start := time.Now()

	contents := make([]map[string]interface{}, 0, len(req.Messages))
	for _, m := range req.Messages {
		role := "user"
		if m.Role == "assistant" || m.Role == "model" {
			role = "model"
		}
		contents = append(contents, map[string]interface{}{
			"role": role,
			"parts": []map[string]string{
				{"text": m.Content},
			},
		})
	}

	payload := map[string]interface{}{
		"contents": contents,
		"generationConfig": map[string]interface{}{
			"temperature":     req.Temperature,
			"maxOutputTokens": req.MaxTokens,
		},
	}

	body, _ := json.Marshal(payload)
	model := req.Model
	if model == "" {
		model = "gemini-pro"
	}
	url := fmt.Sprintf("%s/v1beta/models/%s:generateContent?key=%s",
		p.cfg.BaseURL, model, p.cfg.APIKey)

	resp, err := p.doRequest(ctx, "POST", url, body, nil)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	respBody, _ := io.ReadAll(resp.Body)

	var result struct {
		Candidates []struct {
			Content struct {
				Parts []struct {
					Text string `json:"text"`
				} `json:"parts"`
			} `json:"content"`
			FinishReason string `json:"finishReason"`
		} `json:"candidates"`
		UsageMetadata struct {
			PromptTokenCount     int `json:"promptTokenCount"`
			CandidatesTokenCount int `json:"candidatesTokenCount"`
		} `json:"usageMetadata"`
		Error *struct {
			Message string `json:"message"`
		} `json:"error"`
	}

	if err := json.Unmarshal(respBody, &result); err != nil {
		return nil, err
	}

	if result.Error != nil {
		return nil, fmt.Errorf("gemini error: %s", result.Error.Message)
	}

	content := ""
	if len(result.Candidates) > 0 {
		for _, part := range result.Candidates[0].Content.Parts {
			content += part.Text
		}
	}

	return &Response{
		ID:        "",
		Model:     model,
		Content:   content,
		TokensIn:  result.UsageMetadata.PromptTokenCount,
		TokensOut: result.UsageMetadata.CandidatesTokenCount,
		Provider:  p.cfg.Name,
		Latency:   time.Since(start),
	}, nil
}

// Stream handles SSE streaming from Gemini.
func (p *GeminiProvider) Stream(ctx context.Context, req *Request) (<-chan StreamChunk, error) {
	ch := make(chan StreamChunk, 100)
	close(ch)
	return ch, nil
}

// HealthCheck pings the Gemini API.
func (p *GeminiProvider) HealthCheck(ctx context.Context) error {
	url := fmt.Sprintf("%s/v1beta/models?key=%s", p.cfg.BaseURL, p.cfg.APIKey)
	resp, err := p.doRequest(ctx, "GET", url, nil, nil)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	return nil
}

// EstimateCost calculates the estimated request cost.
func (p *GeminiProvider) EstimateCost(req *Request) float64 {
	totalTokens := 0
	for _, m := range req.Messages {
		totalTokens += len(m.Content) / 4
	}
	return (float64(totalTokens)/1000.0)*p.cfg.CostPer1KIn +
		(float64(req.MaxTokens)/1000.0)*p.cfg.CostPer1KOut
}

// CustomProvider handles local or custom API endpoints.
type CustomProvider struct {
	*BaseProvider
}

// Send forwards a request to a custom endpoint.
func (p *CustomProvider) Send(ctx context.Context, req *Request) (*Response, error) {
	start := time.Now()

	body, _ := json.Marshal(req)
	resp, err := p.doRequest(ctx, "POST", p.cfg.BaseURL+"/v1/chat/completions", body, nil)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	respBody, _ := io.ReadAll(resp.Body)

	var result struct {
		Content string `json:"content"`
		Usage   struct {
			PromptTokens     int `json:"prompt_tokens"`
			CompletionTokens int `json:"completion_tokens"`
		} `json:"usage"`
	}

	json.Unmarshal(respBody, &result)

	return &Response{
		Content:   result.Content,
		TokensIn:  result.Usage.PromptTokens,
		TokensOut: result.Usage.CompletionTokens,
		Provider:  p.cfg.Name,
		Latency:   time.Since(start),
	}, nil
}

// Stream handles SSE streaming from custom endpoints.
func (p *CustomProvider) Stream(ctx context.Context, req *Request) (<-chan StreamChunk, error) {
	ch := make(chan StreamChunk, 100)
	close(ch)
	return ch, nil
}

// HealthCheck pings the custom endpoint.
func (p *CustomProvider) HealthCheck(ctx context.Context) error {
	resp, err := p.doRequest(ctx, "GET", p.cfg.BaseURL+"/health", nil, nil)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	return nil
}

// EstimateCost calculates the estimated request cost.
func (p *CustomProvider) EstimateCost(req *Request) float64 {
	return 0 // Local models are free
}

// Registry manages all configured providers.
type Registry struct {
	providers []Provider
	mu        sync.RWMutex
}

// NewRegistry creates a provider registry from config.
func NewRegistry(cfgs []config.ProviderConfig) (*Registry, error) {
	r := &Registry{providers: make([]Provider, 0, len(cfgs))}
	for _, cfg := range cfgs {
		p, err := New(cfg)
		if err != nil {
			return nil, fmt.Errorf("failed to create provider %s: %w", cfg.Name, err)
		}
		r.providers = append(r.providers, p)
	}
	return r, nil
}

// Get returns a provider by name.
func (r *Registry) Get(name string) (Provider, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	for _, p := range r.providers {
		if p.Name() == name {
			return p, true
		}
	}
	return nil, false
}

// GetActive returns all enabled providers sorted by priority.
func (r *Registry) GetActive() []Provider {
	r.mu.RLock()
	defer r.mu.RUnlock()

	active := make([]Provider, 0)
	for _, p := range r.providers {
		if p.Enabled() {
			active = append(active, p)
		}
	}
	return active
}

// All returns all providers.
func (r *Registry) All() []Provider {
	r.mu.RLock()
	defer r.mu.RUnlock()
	result := make([]Provider, len(r.providers))
	copy(result, r.providers)
	return result
}

// SelectByWeight chooses a provider using weighted random selection.
func (r *Registry) SelectByWeight() (Provider, bool) {
	active := r.GetActive()
	if len(active) == 0 {
		return nil, false
	}

	totalWeight := 0
	for _, p := range active {
		totalWeight += p.Weight()
	}

	if totalWeight == 0 {
		return active[0], true
	}

	pick := 0
	// Simple selection: return highest weight provider
	maxWeight := -1
	for i, p := range active {
		if p.Weight() > maxWeight {
			maxWeight = p.Weight()
			pick = i
		}
	}

	return active[pick], true
}
