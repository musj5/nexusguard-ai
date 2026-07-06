// Copyright 2024 Mustafa Al-Aqrawi (Smile Spoon). All rights reserved.
// Use of this source code is governed by the MIT License
// that can be found in the LICENSE file.

// Package proxy implements the core reverse proxy server that sits between
// the developer's code and AI providers. It orchestrates caching, masking,
// budget tracking, and fallback routing.
package proxy

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/smilespoon/nexusguard-ai/pkg/budget"
	"github.com/smilespoon/nexusguard-ai/pkg/cache"
	"github.com/smilespoon/nexusguard-ai/pkg/config"
	"github.com/smilespoon/nexusguard-ai/pkg/fallback"
	"github.com/smilespoon/nexusguard-ai/pkg/mask"
	"github.com/smilespoon/nexusguard-ai/pkg/providers"
	"github.com/smilespoon/nexusguard-ai/pkg/streaming"
	"go.uber.org/zap"
)

// Stats tracks proxy server metrics.
type Stats struct {
	TotalRequests   int64     `json:"total_requests"`
	SuccessfulReqs  int64     `json:"successful_requests"`
	FailedReqs      int64     `json:"failed_requests"`
	CachedReqs      int64     `json:"cached_requests"`
	MaskedItems     int64     `json:"masked_items"`
	StreamingReqs   int64     `json:"streaming_requests"`
	AvgLatency      time.Duration `json:"avg_latency"`
	ActiveProvider  string    `json:"active_provider"`
	Uptime          time.Time `json:"uptime"`
}

// Server is the reverse proxy server.
type Server struct {
	config       *config.Config
	cache        *cache.Manager
	budget       *budget.Tracker
	masker       *mask.Masker
	registry     *providers.Registry
	fallback     *fallback.Manager
	logger       *zap.Logger
	stats        Stats
	server       *http.Server
	mu           sync.RWMutex
	latencies    []time.Duration
}

// New creates a new proxy server.
func New(
	cfg *config.Config,
	cacheMgr *cache.Manager,
	budgetTracker *budget.Tracker,
	piiMasker *mask.Masker,
	logger *zap.Logger,
) (*Server, error) {
	registry, err := providers.NewRegistry(cfg.Providers)
	if err != nil {
		return nil, fmt.Errorf("failed to create provider registry: %w", err)
	}

	fb := fallback.New(cfg.Fallback, registry)

	s := &Server{
		config:    cfg,
		cache:     cacheMgr,
		budget:    budgetTracker,
		masker:    piiMasker,
		registry:  registry,
		fallback:  fb,
		logger:    logger,
		latencies: make([]time.Duration, 0, 100),
		stats: Stats{
			Uptime: time.Now(),
		},
	}

	mux := http.NewServeMux()
	mux.HandleFunc("/", s.handleRequest)
	mux.HandleFunc("/health", s.handleHealth)
	mux.HandleFunc("/v1/chat/completions", s.handleChatCompletions)
	mux.HandleFunc("/v1/models", s.handleModels)
	mux.HandleFunc("/v1/mask/stats", s.handleMaskStats)
	mux.HandleFunc("/v1/budget/stats", s.handleBudgetStats)
	mux.HandleFunc("/v1/cache/stats", s.handleCacheStats)
	mux.HandleFunc("/v1/proxy/stats", s.handleProxyStats)

	addr := ":" + cfg.Server.Port
	s.server = &http.Server{
		Addr:         addr,
		Handler:      s.corsMiddleware(mux),
		ReadTimeout:  cfg.Server.ReadTimeout,
		WriteTimeout: cfg.Server.WriteTimeout,
		IdleTimeout:  cfg.Server.IdleTimeout,
	}

	return s, nil
}

// Start begins serving requests.
func (s *Server) Start(ctx context.Context) error {
	errChan := make(chan error, 1)

	go func() {
		s.logger.Info("Proxy server listening", zap.String("addr", s.server.Addr))
		if err := s.server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			errChan <- err
		}
	}()

	select {
	case <-ctx.Done():
		shutdownCtx, cancel := context.WithTimeout(context.Background(), s.config.Server.ShutdownTimeout)
		defer cancel()
		s.fallback.Stop()
		return s.server.Shutdown(shutdownCtx)
	case err := <-errChan:
		return err
	}
}

// Stop gracefully shuts down the server.
func (s *Server) Stop() error {
	ctx, cancel := context.WithTimeout(context.Background(), s.config.Server.ShutdownTimeout)
	defer cancel()
	s.fallback.Stop()
	return s.server.Shutdown(ctx)
}

// GetStats returns current proxy statistics.
func (s *Server) GetStats() Stats {
	s.mu.RLock()
	defer s.mu.RUnlock()

	stats := s.stats
	if len(s.latencies) > 0 {
		var sum time.Duration
		for _, l := range s.latencies {
			sum += l
		}
		stats.AvgLatency = sum / time.Duration(len(s.latencies))
	}

	// Get active provider
	if p, ok := s.registry.SelectByWeight(); ok {
		stats.ActiveProvider = p.Name()
	}

	return stats
}

// GetCache returns the cache manager.
func (s *Server) GetCache() *cache.Manager {
	return s.cache
}

// GetBudget returns the budget tracker.
func (s *Server) GetBudget() *budget.Tracker {
	return s.budget
}

// GetMasker returns the PII masker.
func (s *Server) GetMasker() *mask.Masker {
	return s.masker
}

// GetFallback returns the fallback manager.
func (s *Server) GetFallback() *fallback.Manager {
	return s.fallback
}

// GetRegistry returns the provider registry.
func (s *Server) GetRegistry() *providers.Registry {
	return s.registry
}

// Request handlers

func (s *Server) handleRequest(w http.ResponseWriter, r *http.Request) {
	// Default handler - forward to chat completions
	s.handleChatCompletions(w, r)
}

func (s *Server) handleHealth(w http.ResponseWriter, r *http.Request) {
	resp := map[string]interface{}{
		"status":    "healthy",
		"version":   "1.0.0",
		"author":    "Mustafa Al-Aqrawi (Smile Spoon)",
		"uptime":    time.Since(s.stats.Uptime).String(),
		"providers": len(s.registry.GetActive()),
	}
	json.NewEncoder(w).Encode(resp)
}

func (s *Server) handleChatCompletions(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	start := time.Now()
	atomic.AddInt64(&s.stats.TotalRequests, 1)

	// Read request body
	body, err := io.ReadAll(r.Body)
	if err != nil {
		s.logger.Error("Failed to read request body", zap.Error(err))
		http.Error(w, "Bad request", http.StatusBadRequest)
		atomic.AddInt64(&s.stats.FailedReqs, 1)
		return
	}
	defer r.Body.Close()

	// Parse request
	var req providers.Request
	if err := json.Unmarshal(body, &req); err != nil {
		s.logger.Error("Failed to parse request", zap.Error(err))
		http.Error(w, "Invalid JSON", http.StatusBadRequest)
		atomic.AddInt64(&s.stats.FailedReqs, 1)
		return
	}

	// Check budget
	estimatedCost := 0.001 // Default estimation
	if ok, err := s.budget.CanSpend(estimatedCost); !ok {
		s.logger.Warn("Budget limit reached", zap.Error(err))
		http.Error(w, fmt.Sprintf("Budget limit: %v", err), http.StatusPaymentRequired)
		atomic.AddInt64(&s.stats.FailedReqs, 1)
		return
	}

	// Check cache for non-streaming requests
	if !req.Stream {
		if cached, ok := s.cache.Get(&req); ok {
			s.logger.Debug("Cache hit", zap.String("model", req.Model))
			atomic.AddInt64(&s.stats.CachedReqs, 1)
			s.writeResponse(w, cached)
			return
		}
	}

	// Apply PII masking
	maskedBody := body
	var detections []mask.Detection
	if s.masker != nil {
		for i, msg := range req.Messages {
			masked, dets, _ := s.masker.Mask(msg.Content)
			req.Messages[i].Content = masked
			detections = append(detections, dets...)
		}
		maskedBody, _ = json.Marshal(req)
	}

	// Re-parse after masking
	json.Unmarshal(maskedBody, &req)

	// Handle streaming
	if req.Stream {
		atomic.AddInt64(&s.stats.StreamingReqs, 1)
		s.handleStreaming(w, r, &req)
		return
	}

	// Execute via fallback manager
	resp, err := s.fallback.Execute(r.Context(), &req)
	if err != nil {
		s.logger.Error("All providers failed", zap.Error(err))
		http.Error(w, fmt.Sprintf("Proxy error: %v", err), http.StatusBadGateway)
		atomic.AddInt64(&s.stats.FailedReqs, 1)
		return
	}

	// Unmask response content
	if s.masker != nil && resp != nil {
		resp.Content = s.masker.Unmask(resp.Content)
	}

	// Update stats
	latency := time.Since(start)
	atomic.AddInt64(&s.stats.SuccessfulReqs, 1)
	s.recordLatency(latency)

	// Record budget spend
	if resp != nil {
		tokensInCost := float64(resp.TokensIn) / 1000.0 * 0.0015
		tokensOutCost := float64(resp.TokensOut) / 1000.0 * 0.002
		s.budget.RecordSpend(tokensInCost + tokensOutCost)
	}

	// Cache the response
	if resp != nil {
		s.cache.Set(&req, resp, estimatedCost)
	}

	// Update masked items count
	atomic.AddInt64(&s.stats.MaskedItems, int64(len(detections)))

	s.writeResponse(w, resp)
}

func (s *Server) handleStreaming(w http.ResponseWriter, r *http.Request, req *providers.Request) {
	ch, err := s.fallback.ExecuteStream(r.Context(), req)
	if err != nil {
		s.logger.Error("Streaming failed", zap.Error(err))
		http.Error(w, fmt.Sprintf("Streaming error: %v", err), http.StatusBadGateway)
		atomic.AddInt64(&s.stats.FailedReqs, 1)
		return
	}

	sse, err := streaming.NewSSEWriter(w)
	if err != nil {
		s.logger.Error("Failed to create SSE writer", zap.Error(err))
		return
	}

	var content strings.Builder
	for chunk := range ch {
		if chunk.Done {
			sse.WriteDone()
			break
		}

		content.WriteString(chunk.Content)

		// Unmask content if needed
		displayContent := chunk.Content
		if s.masker != nil {
			displayContent = s.masker.Unmask(chunk.Content)
		}

		// Format as OpenAI-compatible chunk
		openAIChunk := map[string]interface{}{
			"object": "chat.completion.chunk",
			"choices": []map[string]interface{}{
				{
					"index": 0,
					"delta": map[string]string{
						"content": displayContent,
					},
					"finish_reason": nil,
				},
			},
		}

		data, _ := json.Marshal(openAIChunk)
		if err := sse.WriteEvent(string(data)); err != nil {
			s.logger.Debug("Client disconnected from stream")
			return
		}
	}

	atomic.AddInt64(&s.stats.SuccessfulReqs, 1)
}

func (s *Server) handleModels(w http.ResponseWriter, r *http.Request) {
	models := []map[string]interface{}{}

	for _, p := range s.registry.GetActive() {
		models = append(models, map[string]interface{}{
			"id":       p.Name(),
			"object":   "model",
			"provider": p.Name(),
			"owned_by": "nexusguard",
		})
	}

	resp := map[string]interface{}{
		"object": "list",
		"data":   models,
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(resp)
}

func (s *Server) handleMaskStats(w http.ResponseWriter, r *http.Request) {
	if s.masker == nil {
		http.Error(w, "Masking not enabled", http.StatusServiceUnavailable)
		return
	}

	stats := s.masker.Stats()
	json.NewEncoder(w).Encode(stats)
}

func (s *Server) handleBudgetStats(w http.ResponseWriter, r *http.Request) {
	if s.budget == nil {
		http.Error(w, "Budget tracking not enabled", http.StatusServiceUnavailable)
		return
	}

	stats := s.budget.GetStats()
	json.NewEncoder(w).Encode(stats)
}

func (s *Server) handleCacheStats(w http.ResponseWriter, r *http.Request) {
	if s.cache == nil {
		http.Error(w, "Cache not enabled", http.StatusServiceUnavailable)
		return
	}

	stats := s.cache.Stats()
	json.NewEncoder(w).Encode(stats)
}

func (s *Server) handleProxyStats(w http.ResponseWriter, r *http.Request) {
	stats := s.GetStats()
	json.NewEncoder(w).Encode(stats)
}

// Helper methods

func (s *Server) writeResponse(w http.ResponseWriter, resp *providers.Response) {
	w.Header().Set("Content-Type", "application/json")

	// Format as OpenAI-compatible response
	openAIResp := map[string]interface{}{
		"id":      resp.ID,
		"object":  "chat.completion",
		"model":   resp.Model,
		"created": time.Now().Unix(),
		"choices": []map[string]interface{}{
			{
				"index": 0,
				"message": map[string]string{
					"role":    "assistant",
					"content": resp.Content,
				},
				"finish_reason": "stop",
			},
		},
		"usage": map[string]int{
			"prompt_tokens":     resp.TokensIn,
			"completion_tokens": resp.TokensOut,
			"total_tokens":      resp.TokensIn + resp.TokensOut,
		},
		"provider": resp.Provider,
		"cached":   resp.Cached,
	}

	json.NewEncoder(w).Encode(openAIResp)
}

func (s *Server) recordLatency(d time.Duration) {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.latencies = append(s.latencies, d)
	if len(s.latencies) > 100 {
		s.latencies = s.latencies[1:]
	}
}

func (s *Server) corsMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Access-Control-Allow-Origin", "*")
		w.Header().Set("Access-Control-Allow-Methods", "GET, POST, PUT, DELETE, OPTIONS")
		w.Header().Set("Access-Control-Allow-Headers", "Content-Type, Authorization, X-Request-ID")
		w.Header().Set("Access-Control-Allow-Credentials", "true")

		if r.Method == http.MethodOptions {
			w.WriteHeader(http.StatusOK)
			return
		}

		next.ServeHTTP(w, r)
	})
}
