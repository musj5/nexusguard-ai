// Copyright 2026 Mustafa Al-Aqrawi (Smile Spoon). All rights reserved.
// Use of this source code is governed by the MIT License
// that can be found in the LICENSE file.

package utils

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"net/http"
	"time"
)

// GenerateID creates a unique request identifier.
func GenerateID() string {
	b := make([]byte, 8)
	rand.Read(b)
	return hex.EncodeToString(b)
}

// Retry executes a function with exponential backoff.
func Retry(attempts int, delay time.Duration, fn func() error) error {
	var err error
	for i := 0; i < attempts; i++ {
		err = fn()
		if err == nil {
			return nil
		}
		time.Sleep(delay * time.Duration(i+1))
	}
	return fmt.Errorf("failed after %d attempts: %w", attempts, err)
}

// Contains checks if a string slice contains a value.
func Contains(slice []string, item string) bool {
	for _, s := range slice {
		if s == item {
			return true
		}
	}
	return false
}

// Min returns the minimum of two integers.
func Min(a, b int) int {
	if a < b {
		return a
	}
	return b
}

// Max returns the maximum of two integers.
func Max(a, b int) int {
	if a > b {
		return a
	}
	return b
}

// SanitizeString removes potentially harmful characters.
func SanitizeString(s string) string {
	if len(s) > 10000 {
		s = s[:10000]
	}
	return s
}

// IsValidAPIKey checks if an API key format is valid.
func IsValidAPIKey(key string) bool {
	return len(key) > 10 && len(key) < 512
}

// GetClientIP extracts the real client IP from a request.
func GetClientIP(r *http.Request) string {
	xff := r.Header.Get("X-Forwarded-For")
	if xff != "" {
		return xff
	}

	xri := r.Header.Get("X-Real-Ip")
	if xri != "" {
		return xri
	}

	return r.RemoteAddr
}

// TruncateString truncates a string to max length with ellipsis.
func TruncateString(s string, maxLen int) string {
	if len(s) <= maxLen {
		return s
	}
	if maxLen <= 3 {
		return s[:maxLen]
	}
	return s[:maxLen-3] + "..."
}

// FormatDuration formats a duration to human-readable string.
func FormatDuration(d time.Duration) string {
	if d < time.Millisecond {
		return fmt.Sprintf("%dμs", d.Microseconds())
	}
	if d < time.Second {
		return fmt.Sprintf("%dms", d.Milliseconds())
	}
	return fmt.Sprintf("%.2fs", d.Seconds())
}

// Round rounds a float64 to given decimal places.
func Round(val float64, precision int) float64 {
	p := 1.0
	for i := 0; i < precision; i++ {
		p *= 10
	}
	return float64(int(val*p+0.5)) / p
}
