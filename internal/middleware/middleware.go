// Copyright 2024 Mustafa Al-Aqrawi (Smile Spoon). All rights reserved.
// Use of this source code is governed by the MIT License
// that can be found in the LICENSE file.

package middleware

import (
	"net/http"
	"time"

	"github.com/smilespoon/nexusguard-ai/internal/utils"
	"go.uber.org/zap"
)

// Logging middleware logs HTTP requests.
func Logging(logger *zap.Logger) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			start := time.Now()
			requestID := utils.GenerateID()

			// Add request ID to response headers
			w.Header().Set("X-Request-ID", requestID)

			// Wrap response writer to capture status code
			wrapped := &responseWriter{ResponseWriter: w, statusCode: http.StatusOK}

			next.ServeHTTP(wrapped, r)

			logger.Info("HTTP request",
				zap.String("request_id", requestID),
				zap.String("method", r.Method),
				zap.String("path", r.URL.Path),
				zap.Int("status", wrapped.statusCode),
				zap.Duration("duration", time.Since(start)),
				zap.String("client_ip", utils.GetClientIP(r)),
				zap.String("user_agent", r.UserAgent()),
			)
		})
	}
}

// Recovery middleware recovers from panics.
func Recovery(logger *zap.Logger) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			defer func() {
				if err := recover(); err != nil {
					logger.Error("Panic recovered",
						zap.Any("error", err),
						zap.String("path", r.URL.Path),
					)
					http.Error(w, "Internal Server Error", http.StatusInternalServerError)
				}
			}()
			next.ServeHTTP(w, r)
		})
	}
}

// RateLimiter implements a simple token bucket rate limiter.
type RateLimiter struct {
	tokens   chan struct{}
	interval time.Duration
}

// NewRateLimiter creates a rate limiter allowing burst requests.
func NewRateLimiter(rps int) *RateLimiter {
	return &RateLimiter{
		tokens:   make(chan struct{}, rps),
		interval: time.Second / time.Duration(rps),
	}
}

// Middleware returns the rate limiting middleware.
func (rl *RateLimiter) Middleware() func(http.Handler) http.Handler {
	// Fill tokens
	go func() {
		ticker := time.NewTicker(rl.interval)
		defer ticker.Stop()
		for range ticker.C {
			select {
			case rl.tokens <- struct{}{}:
			default:
			}
		}
	}()

	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			select {
			case <-rl.tokens:
				next.ServeHTTP(w, r)
			default:
				http.Error(w, "Rate limit exceeded", http.StatusTooManyRequests)
			}
		})
	}
}

// Chain chains multiple middleware together.
func Chain(middlewares ...func(http.Handler) http.Handler) func(http.Handler) http.Handler {
	return func(final http.Handler) http.Handler {
		for i := len(middlewares) - 1; i >= 0; i-- {
			final = middlewares[i](final)
		}
		return final
	}
}

// responseWriter wraps http.ResponseWriter to capture status code.
type responseWriter struct {
	http.ResponseWriter
	statusCode int
	written    bool
}

func (rw *responseWriter) WriteHeader(code int) {
	if !rw.written {
		rw.statusCode = code
		rw.written = true
		rw.ResponseWriter.WriteHeader(code)
	}
}
