// Copyright 2024 Mustafa Al-Aqrawi (Smile Spoon). All rights reserved.
// Use of this source code is governed by the MIT License
// that can be found in the LICENSE file.

// Package fallback implements Smart Auto-Fallback — when the primary AI provider
// fails, requests are seamlessly routed to backup providers without crashing.
package fallback

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/smilespoon/nexusguard-ai/pkg/config"
	"github.com/smilespoon/nexusguard-ai/pkg/providers"
)

// CircuitState represents the circuit breaker state.
type CircuitState int

const (
	CircuitClosed   CircuitState = iota // Normal operation
	CircuitOpen                         // Failing fast
	CircuitHalfOpen                     // Testing recovery
)

// HealthStatus tracks provider health.
type HealthStatus struct {
	Provider      string        `json:"provider"`
	Healthy       bool          `json:"healthy"`
	LastCheck     time.Time     `json:"last_check"`
	Failures      int           `json:"failures"`
	SuccessRate   float64       `json:"success_rate"`
	AvgLatency    time.Duration `json:"avg_latency"`
	CircuitState  CircuitState  `json:"circuit_state"`
}

// FallbackStats tracks failover metrics.
type FallbackStats struct {
	TotalRequests    int64            `json:"total_requests"`
	Successful       int64            `json:"successful"`
	Failed           int64            `json:"failed"`
	Fallbacks        int64            `json:"fallbacks"`
	CircuitBreaks    int64            `json:"circuit_breaks"`
	ProviderHealth   map[string]*HealthStatus `json:"provider_health"`
}

// Manager handles provider failover and circuit breaking.
type Manager struct {
	config      config.FallbackConfig
	registry    *providers.Registry
	health      map[string]*HealthStatus
	stats       FallbackStats
	mu          sync.RWMutex
	stopChan    chan struct{}
}

// New creates a fallback manager.
func New(cfg config.FallbackConfig, registry *providers.Registry) *Manager {
	m := &Manager{
		config:    cfg,
		registry:  registry,
		health:    make(map[string]*HealthStatus),
		stopChan:  make(chan struct{}),
		stats: FallbackStats{
			ProviderHealth: make(map[string]*HealthStatus),
		},
	}

	// Initialize health status for all providers
	for _, p := range registry.All() {
		m.health[p.Name()] = &HealthStatus{
			Provider:     p.Name(),
			Healthy:      true,
			CircuitState: CircuitClosed,
		}
		m.stats.ProviderHealth[p.Name()] = m.health[p.Name()]
	}

	// Start health check loop
	go m.healthCheckLoop()

	return m
}

// Execute attempts a request with automatic failover.
func (m *Manager) Execute(ctx context.Context, req *providers.Request) (*providers.Response, error) {
	if !m.config.Enabled {
		// Get first active provider and execute directly
		active := m.registry.GetActive()
		if len(active) == 0 {
			return nil, fmt.Errorf("no active providers available")
		}
		m.stats.TotalRequests++
		resp, err := active[0].Send(ctx, req)
		if err != nil {
			m.stats.Failed++
			return nil, err
		}
		m.stats.Successful++
		return resp, nil
	}

	m.stats.TotalRequests++

	// Try providers in priority order
	candidates := m.getCandidates()

	var lastErr error
	for _, candidate := range candidates {
		resp, err := m.tryProvider(ctx, candidate, req)
		if err == nil {
			m.stats.Successful++
			if candidate.Name() != candidates[0].Name() {
				m.stats.Fallbacks++
			}
			return resp, nil
		}

		lastErr = err
		m.recordFailure(candidate.Name())

		// Circuit breaker check
		if m.shouldOpenCircuit(candidate.Name()) {
			m.openCircuit(candidate.Name())
		}
	}

	m.stats.Failed++
	return nil, fmt.Errorf("all providers failed, last error: %w", lastErr)
}

// ExecuteStream attempts streaming with failover.
func (m *Manager) ExecuteStream(ctx context.Context, req *providers.Request) (<-chan providers.StreamChunk, error) {
	if !m.config.Enabled {
		active := m.registry.GetActive()
		if len(active) == 0 {
			return nil, fmt.Errorf("no active providers available")
		}
		return active[0].Stream(ctx, req)
	}

	candidates := m.getCandidates()

	var lastErr error
	for _, candidate := range candidates {
		// Update health status
		health := m.health[candidate.Name()]
		if health != nil && health.CircuitState == CircuitOpen {
			continue
		}

		ch, err := candidate.Stream(ctx, req)
		if err == nil {
			m.recordSuccess(candidate.Name())
			return ch, nil
		}

		lastErr = err
		m.recordFailure(candidate.Name())
	}

	return nil, fmt.Errorf("all providers failed streaming, last error: %w", lastErr)
}

// Stats returns current fallback statistics.
func (m *Manager) Stats() FallbackStats {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.stats
}

// Stop halts the health check loop.
func (m *Manager) Stop() {
	close(m.stopChan)
}

// internal methods

func (m *Manager) tryProvider(ctx context.Context, p providers.Provider, req *providers.Request) (*providers.Response, error) {
	health := m.health[p.Name()]
	if health != nil && health.CircuitState == CircuitOpen {
		return nil, fmt.Errorf("circuit breaker open for %s", p.Name())
	}

	// Create a timeout context for this attempt
	attemptCtx, cancel := context.WithTimeout(ctx, m.config.Timeout)
	defer cancel()

	resp, err := p.Send(attemptCtx, req)
	if err != nil {
		return nil, err
	}

	m.recordSuccess(p.Name())
	return resp, nil
}

func (m *Manager) getCandidates() []providers.Provider {
	active := m.registry.GetActive()

	// Filter out providers with open circuits
	var candidates []providers.Provider
	for _, p := range active {
		health := m.health[p.Name()]
		if health == nil || health.CircuitState != CircuitOpen {
			candidates = append(candidates, p)
		}
	}

	// If all circuits are open, try them all as last resort
	if len(candidates) == 0 {
		candidates = active
	}

	return candidates
}

func (m *Manager) recordSuccess(name string) {
	m.mu.Lock()
	defer m.mu.Unlock()

	if h, ok := m.health[name]; ok {
		h.Healthy = true
		h.Failures = 0
		h.SuccessRate = 1.0
		if h.CircuitState == CircuitHalfOpen {
			h.CircuitState = CircuitClosed
		}
	}
}

func (m *Manager) recordFailure(name string) {
	m.mu.Lock()
	defer m.mu.Unlock()

	if h, ok := m.health[name]; ok {
		h.Failures++
		h.Healthy = false
		// Decay success rate
		h.SuccessRate *= 0.9
	}
}

func (m *Manager) shouldOpenCircuit(name string) bool {
	h := m.health[name]
	if h == nil {
		return false
	}
	return h.Failures >= m.config.CircuitBreakerThreshold && h.CircuitState == CircuitClosed
}

func (m *Manager) openCircuit(name string) {
	if h, ok := m.health[name]; ok {
		h.CircuitState = CircuitOpen
		m.stats.CircuitBreaks++

		// Schedule half-open after cooldown
		go func() {
			time.Sleep(30 * time.Second)
			m.mu.Lock()
			h.CircuitState = CircuitHalfOpen
			m.mu.Unlock()
		}()
	}
}

func (m *Manager) healthCheckLoop() {
	ticker := time.NewTicker(m.config.HealthCheckInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ticker.C:
			m.runHealthChecks()
		case <-m.stopChan:
			return
		}
	}
}

func (m *Manager) runHealthChecks() {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	for _, p := range m.registry.All() {
		go func(provider providers.Provider) {
			err := provider.HealthCheck(ctx)
			m.mu.Lock()
			defer m.mu.Unlock()

			h := m.health[provider.Name()]
			if h == nil {
				return
			}

			h.LastCheck = time.Now()
			if err != nil {
				h.Healthy = false
			} else {
				h.Healthy = true
				if h.CircuitState == CircuitOpen {
					h.CircuitState = CircuitHalfOpen
				}
			}
		}(p)
	}
}
