// Copyright 2024 Mustafa Al-Aqrawi (Smile Spoon). All rights reserved.
// Use of this source code is governed by the MIT License
// that can be found in the LICENSE file.

// Package budget implements the Budget Defender feature — a real-time spending
// guardrail that prevents runaway API costs by enforcing configurable limits.
package budget

import (
	"fmt"
	"sync"
	"time"

	"github.com/smilespoon/nexusguard-ai/pkg/config"
	"go.uber.org/atomic"
)

// Status represents the current budget state.
type Status int

const (
	StatusOK       Status = iota // Within budget
	StatusWarning                 // Approaching limit
	StatusExceeded                // Budget exceeded
	StatusBlocked                 // Hard stop enforced
)

func (s Status) String() string {
	switch s {
	case StatusOK:
		return "OK"
	case StatusWarning:
		return "WARNING"
	case StatusExceeded:
		return "EXCEEDED"
	case StatusBlocked:
		return "BLOCKED"
	default:
		return "UNKNOWN"
	}
}

// Stats tracks budget metrics.
type Stats struct {
	DailySpent    float64   `json:"daily_spent"`
	MonthlySpent  float64   `json:"monthly_spent"`
	DailyLimit    float64   `json:"daily_limit"`
	MonthlyLimit  float64   `json:"monthly_limit"`
	Status        Status    `json:"status"`
	RemainingDay  float64   `json:"remaining_day"`
	RemainingMonth float64  `json:"remaining_month"`
	LastReset     time.Time `json:"last_reset"`
	RequestCount  int64     `json:"request_count"`
}

// Tracker monitors and enforces spending limits.
type Tracker struct {
	config       config.BudgetConfig
	dailySpent   atomic.Float64
	monthlySpent atomic.Float64
	status       atomic.Value
	lastReset    atomic.Value
	requestCount atomic.Int64
	mu           sync.RWMutex
}

// New creates a budget tracker.
func New(cfg config.BudgetConfig) *Tracker {
	t := &Tracker{
		config: cfg,
	}
	t.status.Store(StatusOK)
	t.lastReset.Store(time.Now())
	return t
}

// CanSpend checks if a request can proceed within budget.
func (t *Tracker) CanSpend(estimatedCost float64) (bool, error) {
	if !t.config.Enabled {
		return true, nil
	}

	t.mu.RLock()
	defer t.mu.RUnlock()

	daily := t.dailySpent.Load()
	monthly := t.monthlySpent.Load()

	// Check if already exceeded
	if t.config.HardStop {
		if daily >= t.config.DailyLimit || monthly >= t.config.MonthlyLimit {
			t.status.Store(StatusBlocked)
			return false, fmt.Errorf("budget limit exceeded: daily=$%.2f/$%.2f, monthly=$%.2f/$%.2f",
				daily, t.config.DailyLimit, monthly, t.config.MonthlyLimit)
		}
	}

	// Check if this request would exceed the limit
	if daily+estimatedCost > t.config.DailyLimit {
		t.status.Store(StatusExceeded)
		if t.config.HardStop {
			return false, fmt.Errorf("request would exceed daily budget: $%.2f + $%.2f > $%.2f",
				daily, estimatedCost, t.config.DailyLimit)
		}
	}

	if monthly+estimatedCost > t.config.MonthlyLimit {
		t.status.Store(StatusExceeded)
		if t.config.HardStop {
			return false, fmt.Errorf("request would exceed monthly budget: $%.2f + $%.2f > $%.2f",
				monthly, estimatedCost, t.config.MonthlyLimit)
		}
	}

	// Update status based on thresholds
	dailyRatio := (daily + estimatedCost) / t.config.DailyLimit
	if dailyRatio >= t.config.WarningThreshold {
		t.status.Store(StatusWarning)
	} else {
		t.status.Store(StatusOK)
	}

	return true, nil
}

// RecordSpend logs actual spending after a successful request.
func (t *Tracker) RecordSpend(cost float64) {
	if !t.config.Enabled {
		return
	}

	t.dailySpent.Add(cost)
	t.monthlySpent.Add(cost)
	t.requestCount.Inc()
}

// GetStats returns current budget statistics.
func (t *Tracker) GetStats() *Stats {
	daily := t.dailySpent.Load()
	monthly := t.monthlySpent.Load()

	return &Stats{
		DailySpent:     daily,
		MonthlySpent:   monthly,
		DailyLimit:     t.config.DailyLimit,
		MonthlyLimit:   t.config.MonthlyLimit,
		Status:         t.status.Load().(Status),
		RemainingDay:   t.config.DailyLimit - daily,
		RemainingMonth: t.config.MonthlyLimit - monthly,
		LastReset:      t.lastReset.Load().(time.Time),
		RequestCount:   t.requestCount.Load(),
	}
}

// Status returns the current budget status.
func (t *Tracker) Status() Status {
	return t.status.Load().(Status)
}

// ResetDaily resets the daily spending counter.
func (t *Tracker) ResetDaily() {
	t.dailySpent.Store(0)
	t.lastReset.Store(time.Now())
	t.status.Store(StatusOK)
}

// ResetMonthly resets the monthly spending counter.
func (t *Tracker) ResetMonthly() {
	t.monthlySpent.Store(0)
	t.ResetDaily()
}

// EstimateTokens estimates cost from token counts.
func (t *Tracker) EstimateTokens(tokensIn, tokensOut int, costPer1KIn, costPer1KOut float64) float64 {
	return (float64(tokensIn)/1000.0)*costPer1KIn + (float64(tokensOut)/1000.0)*costPer1KOut
}

// ForceBlock forces the budget into blocked state (emergency stop).
func (t *Tracker) ForceBlock() {
	t.status.Store(StatusBlocked)
}

// Unblock resets the blocked state (manual override).
func (t *Tracker) Unblock() {
	t.status.Store(StatusOK)
}

// SetDailyLimit updates the daily limit at runtime.
func (t *Tracker) SetDailyLimit(limit float64) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.config.DailyLimit = limit
}

// SetMonthlyLimit updates the monthly limit at runtime.
func (t *Tracker) SetMonthlyLimit(limit float64) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.config.MonthlyLimit = limit
}
