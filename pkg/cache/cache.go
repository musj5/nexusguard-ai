// Copyright 2026 Mustafa Al-Aqrawi (Smile Spoon). All rights reserved.
// Use of this source code is governed by the MIT License
// that can be found in the LICENSE file.

// Package cache implements a high-speed semantic caching layer using BadgerDB.
// It caches LLM responses to save money and reduce latency on repeated queries.
package cache

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/dgraph-io/badger/v4"
	"github.com/smilespoon/nexusguard-ai/pkg/config"
	"github.com/smilespoon/nexusguard-ai/pkg/providers"
)

// Stats holds cache performance metrics.
type Stats struct {
	Hits       int64   `json:"hits"`
	Misses     int64   `json:"misses"`
	Size       int64   `json:"size"`
	SavedCost  float64 `json:"saved_cost"`
	HitRate    float64 `json:"hit_rate"`
	mu         sync.RWMutex
}

// CacheEntry stores a cached response with metadata.
type CacheEntry struct {
	Key        string             `json:"key"`
	Request    string             `json:"request"`
	Response   *providers.Response `json:"response"`
	CreatedAt  time.Time          `json:"created_at"`
	ExpiresAt  time.Time          `json:"expires_at"`
	AccessCount int64             `json:"access_count"`
	CostSaved  float64            `json:"cost_saved"`
}

// Manager handles all caching operations.
type Manager struct {
	db     *badger.DB
	stats  *Stats
	config config.CacheConfig
	mu     sync.RWMutex
}

// New creates a new cache manager.
func New(cfg config.CacheConfig) (*Manager, error) {
	if !cfg.Enabled {
		return &Manager{stats: &Stats{}}, nil
	}

	opts := badger.DefaultOptions(cfg.Path).
		WithLogger(nil).
		WithBaseTableSize(4 << 20).
		WithNumMemtables(2).
		WithNumLevelZeroTables(2).
		WithSyncWrites(false)

	db, err := badger.Open(opts)
	if err != nil {
		return nil, fmt.Errorf("failed to open cache db: %w", err)
	}

	m := &Manager{
		db:     db,
		stats:  &Stats{},
		config: cfg,
	}

	// Start cleanup goroutine
	go m.cleanupLoop()

	return m, nil
}

// Close shuts down the cache.
func (m *Manager) Close() error {
	if m.db != nil {
		return m.db.Close()
	}
	return nil
}

// Get retrieves a cached response by request hash.
func (m *Manager) Get(req *providers.Request) (*providers.Response, bool) {
	if !m.config.Enabled {
		m.stats.mu.Lock()
		m.stats.Misses++
		m.stats.mu.Unlock()
		return nil, false
	}

	key := m.hashRequest(req)

	var entry CacheEntry
	err := m.db.View(func(txn *badger.Txn) error {
		item, err := txn.Get([]byte(key))
		if err != nil {
			return err
		}
		return item.Value(func(val []byte) error {
			return json.Unmarshal(val, &entry)
		})
	})

	if err != nil {
		m.stats.mu.Lock()
		m.stats.Misses++
		m.stats.mu.Unlock()
		return nil, false
	}

	// Check expiry
	if time.Now().After(entry.ExpiresAt) {
		m.delete(key)
		m.stats.mu.Lock()
		m.stats.Misses++
		m.stats.mu.Unlock()
		return nil, false
	}

	// Update access count
	entry.AccessCount++
	m.putEntry(key, &entry)

	// Update stats
	m.stats.mu.Lock()
	m.stats.Hits++
	m.stats.SavedCost += entry.CostSaved
	m.updateHitRate()
	m.stats.mu.Unlock()

	// Mark as cached
	entry.Response.Cached = true

	return entry.Response, true
}

// Set stores a response in the cache.
func (m *Manager) Set(req *providers.Request, resp *providers.Response, cost float64) error {
	if !m.config.Enabled {
		return nil
	}

	key := m.hashRequest(req)

	entry := &CacheEntry{
		Key:         key,
		Request:     m.serializeRequest(req),
		Response:    resp,
		CreatedAt:   time.Now(),
		ExpiresAt:   time.Now().Add(m.config.TTL),
		AccessCount: 1,
		CostSaved:   cost,
	}

	return m.putEntry(key, entry)
}

// GetSimilar tries to find a semantically similar cached request.
func (m *Manager) GetSimilar(req *providers.Request, threshold float64) (*providers.Response, bool) {
	if !m.config.Enabled {
		return nil, false
	}

	// Simple implementation: normalize and compare
	reqNorm := m.normalizeRequest(req)
	var bestMatch *CacheEntry
	bestScore := 0.0

	err := m.db.View(func(txn *badger.Txn) error {
		opts := badger.DefaultIteratorOptions
		opts.PrefetchSize = 10
		it := txn.NewIterator(opts)
		defer it.Close()

		for it.Rewind(); it.Valid(); it.Next() {
			item := it.Item()
			var entry CacheEntry
			err := item.Value(func(val []byte) error {
				return json.Unmarshal(val, &entry)
			})
			if err != nil {
				continue
			}

			// Check expiry
			if time.Now().After(entry.ExpiresAt) {
				continue
			}

			// Calculate similarity
			similarity := m.calculateSimilarity(reqNorm, entry.Request)
			if similarity > bestScore && similarity >= threshold {
				bestScore = similarity
				bestMatch = &entry
			}
		}
		return nil
	})

	if err != nil || bestMatch == nil {
		m.stats.mu.Lock()
		m.stats.Misses++
		m.stats.mu.Unlock()
		return nil, false
	}

	m.stats.mu.Lock()
	m.stats.Hits++
	m.stats.SavedCost += bestMatch.CostSaved * float64(bestMatch.AccessCount)
	m.updateHitRate()
	m.stats.mu.Unlock()

	bestMatch.Response.Cached = true
	return bestMatch.Response, true
}

// Stats returns current cache statistics.
func (m *Manager) Stats() *Stats {
	m.stats.mu.RLock()
	defer m.stats.mu.RUnlock()

	// Return a copy
	s := *m.stats
	return &s
}

// Clear removes all cached entries.
func (m *Manager) Clear() error {
	if !m.config.Enabled || m.db == nil {
		return nil
	}

	m.stats.mu.Lock()
	m.stats.Hits = 0
	m.stats.Misses = 0
	m.stats.SavedCost = 0
	m.stats.HitRate = 0
	m.stats.mu.Unlock()

	return m.db.DropAll()
}

// internal methods

func (m *Manager) putEntry(key string, entry *CacheEntry) error {
	data, err := json.Marshal(entry)
	if err != nil {
		return err
	}

	return m.db.Update(func(txn *badger.Txn) error {
		return txn.Set([]byte(key), data)
	})
}

func (m *Manager) delete(key string) {
	m.db.Update(func(txn *badger.Txn) error {
		return txn.Delete([]byte(key))
	})
}

func (m *Manager) hashRequest(req *providers.Request) string {
	h := sha256.New()

	// Hash model and messages
	h.Write([]byte(req.Model))
	for _, msg := range req.Messages {
		h.Write([]byte(msg.Role))
		h.Write([]byte(msg.Content))
	}
	h.Write([]byte(fmt.Sprintf("%f", req.Temperature)))
	h.Write([]byte(fmt.Sprintf("%d", req.MaxTokens)))

	return hex.EncodeToString(h.Sum(nil))
}

func (m *Manager) serializeRequest(req *providers.Request) string {
	data, _ := json.Marshal(req)
	return string(data)
}

func (m *Manager) normalizeRequest(req *providers.Request) string {
	var parts []string
	for _, msg := range req.Messages {
		parts = append(parts, strings.ToLower(strings.TrimSpace(msg.Content)))
	}
	return strings.Join(parts, " ")
}

func (m *Manager) calculateSimilarity(a, b string) float64 {
	// Simple Jaccard similarity on word sets
	wordsA := tokenize(a)
	wordsB := tokenize(b)

	if len(wordsA) == 0 || len(wordsB) == 0 {
		return 0
	}

	intersection := 0
	for w := range wordsA {
		if wordsB[w] {
			intersection++
		}
	}

	union := len(wordsA) + len(wordsB) - intersection
	if union == 0 {
		return 0
	}

	return float64(intersection) / float64(union)
}

func tokenize(text string) map[string]bool {
	words := make(map[string]bool)
	for _, w := range strings.Fields(text) {
		w = strings.ToLower(strings.TrimSpace(w))
		if len(w) > 2 {
			words[w] = true
		}
	}
	return words
}

func (m *Manager) updateHitRate() {
	total := m.stats.Hits + m.stats.Misses
	if total > 0 {
		m.stats.HitRate = float64(m.stats.Hits) / float64(total)
	}
}

func (m *Manager) cleanupLoop() {
	ticker := time.NewTicker(time.Hour)
	defer ticker.Stop()

	for range ticker.C {
		m.db.RunValueLogGC(0.5)
	}
}
