// Copyright 2026 Mustafa Al-Aqrawi (Smile Spoon). All rights reserved.
// Use of this source code is governed by the MIT License
// that can be found in the LICENSE file.

// Package mask implements bi-directional PII (Personally Identifiable Information)
// detection and masking. It protects sensitive data before sending to AI APIs
// and seamlessly restores it in responses.
package mask

import (
	"fmt"
	"regexp"
	"strings"
	"sync"

	"github.com/smilespoon/nexusguard-ai/pkg/config"
)

// PIIType identifies the category of detected PII.
type PIIType string

const (
	PIIEmail       PIIType = "email"
	PIIPhone       PIIType = "phone"
	PIICreditCard  PIIType = "credit_card"
	PIISSN         PIIType = "ssn"
	PIIAPIKey      PIIType = "api_key"
	PIIIP          PIIType = "ip_address"
	PIICustom      PIIType = "custom"
)

// Detection represents a single PII finding.
type Detection struct {
	Type      PIIType `json:"type"`
	Original  string  `json:"original"`
	Masked    string  `json:"masked"`
	Position  int     `json:"position"`
	Length    int     `json:"length"`
}

// Stats tracks PII masking metrics.
type Stats struct {
	EmailsMasked      int64 `json:"emails_masked"`
	PhonesMasked      int64 `json:"phones_masked"`
	CreditCardsMasked int64 `json:"credit_cards_masked"`
	SSNsMasked        int64 `json:"ssns_masked"`
	APIKeysMasked     int64 `json:"api_keys_masked"`
	IPsMasked         int64 `json:"ips_masked"`
	CustomMasked      int64 `json:"custom_masked"`
	TotalMasked       int64 `json:"total_masked"`
}

// Masker handles PII detection and masking operations.
type Masker struct {
	config    config.MaskConfig
	patterns  map[PIIType]*regexp.Regexp
	mappings  map[string]string // mask -> original (for unmasking)
	reverse   map[string]string // original -> mask
	mu        sync.RWMutex
	stats     Stats
}

// New creates a new PII masker.
func New(cfg config.MaskConfig) *Masker {
	m := &Masker{
		config:   cfg,
		patterns: make(map[PIIType]*regexp.Regexp),
		mappings: make(map[string]string),
		reverse:  make(map[string]string),
	}

	m.compilePatterns()
	return m
}

// Mask detects and masks all PII in the given text.
func (m *Masker) Mask(text string) (string, []Detection, error) {
	if !m.config.Enabled {
		return text, nil, nil
	}

	var detections []Detection
	result := text

	m.mu.Lock()
	defer m.mu.Unlock()

	// Track replacements to avoid conflicts
	replacements := make(map[string]string)
	counter := make(map[PIIType]int)

	// Apply each pattern
	for piiType, pattern := range m.patterns {
		matches := pattern.FindAllStringIndex(result, -1)
		if matches == nil {
			continue
		}

		// Process matches in reverse order to preserve positions
		for i := len(matches) - 1; i >= 0; i-- {
			match := matches[i]
			original := result[match[0]:match[1]]

			// Check if already replaced
			if _, exists := replacements[original]; exists {
				continue
			}

			counter[piiType]++
			placeholder := m.generatePlaceholder(piiType, counter[piiType])

			replacements[original] = placeholder
			m.mappings[placeholder] = original
			m.reverse[original] = placeholder

			detections = append(detections, Detection{
				Type:     piiType,
				Original: original,
				Masked:   placeholder,
				Position: match[0],
				Length:   match[1] - match[0],
			})
		}
	}

	// Apply replacements
	for original, placeholder := range replacements {
		result = strings.ReplaceAll(result, original, placeholder)
	}

	// Update stats
	m.updateStats(detections)

	return result, detections, nil
}

// Unmask restores all masked PII in the given text.
func (m *Masker) Unmask(text string) string {
	if !m.config.Enabled {
		return text
	}

	m.mu.RLock()
	defer m.mu.RUnlock()

	result := text
	for placeholder, original := range m.mappings {
		result = strings.ReplaceAll(result, placeholder, original)
	}

	return result
}

// MaskJSON masks PII within JSON string values.
func (m *Masker) MaskJSON(data []byte) ([]byte, []Detection, error) {
	// Simple string-based masking for JSON content
	text := string(data)
	masked, detections, err := m.Mask(text)
	return []byte(masked), detections, err
}

// Stats returns current masking statistics.
func (m *Masker) Stats() Stats {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.stats
}

// Reset clears all mappings and stats.
func (m *Masker) Reset() {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.mappings = make(map[string]string)
	m.reverse = make(map[string]string)
	m.stats = Stats{}
}

// compilePatterns compiles all PII regex patterns.
func (m *Masker) compilePatterns() {
	if m.config.MaskEmails {
		m.patterns[PIIEmail] = regexp.MustCompile(`(?i)[a-z0-9._%+-]+@[a-z0-9.-]+\.[a-z]{2,}`)
	}
	if m.config.MaskPhones {
		// International and US formats
		m.patterns[PIIPhone] = regexp.MustCompile(`(?:\+?\d{1,3}[-.\s]?)?\(?\d{3}\)?[-.\s]?\d{3}[-.\s]?\d{4}`)
	}
	if m.config.MaskCreditCards {
		// Major credit card patterns
		m.patterns[PIICreditCard] = regexp.MustCompile(`(?:\d{4}[-\s]?){3}\d{4}`)
	}
	if m.config.MaskSSN {
		// US SSN pattern
		m.patterns[PIISSN] = regexp.MustCompile(`\b\d{3}[-\s]?\d{2}[-\s]?\d{4}\b`)
	}
	if m.config.MaskAPIKeys {
		// Common API key patterns
		m.patterns[PIIAPIKey] = regexp.MustCompile(`(?i)(?:api[_-]?key|apikey|token)[\s]*[=:]\s*['"]?([a-zA-Z0-9_-]{16,})['"]?`)
	}
	if m.config.MaskIPs {
		// IPv4 and IPv6 patterns
		m.patterns[PIIIP] = regexp.MustCompile(`\b(?:\d{1,3}\.){3}\d{1,3}\b|\b(?:[0-9a-fA-F]{1,4}:){7}[0-9a-fA-F]{1,4}\b`)
	}

	// Custom patterns
	for i, pattern := range m.config.CustomPatterns {
		re, err := regexp.Compile(pattern)
		if err == nil {
			m.patterns[PIIType(fmt.Sprintf("custom_%d", i))] = re
		}
	}
}

// generatePlaceholder creates a unique placeholder for a PII type.
func (m *Masker) generatePlaceholder(t PIIType, count int) string {
	return fmt.Sprintf("%s<%s_%d>", m.config.Placeholder, strings.ToUpper(string(t)), count)
}

// updateStats increments counters based on detections.
func (m *Masker) updateStats(detections []Detection) {
	m.stats.TotalMasked += int64(len(detections))
	for _, d := range detections {
		switch d.Type {
		case PIIEmail:
			m.stats.EmailsMasked++
		case PIIPhone:
			m.stats.PhonesMasked++
		case PIICreditCard:
			m.stats.CreditCardsMasked++
		case PIISSN:
			m.stats.SSNsMasked++
		case PIIAPIKey:
			m.stats.APIKeysMasked++
		case PIIIP:
			m.stats.IPsMasked++
		default:
			if strings.HasPrefix(string(d.Type), "custom") {
				m.stats.CustomMasked++
			}
		}
	}
}
