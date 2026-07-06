// Copyright 2026 Mustafa Al-Aqrawi (Smile Spoon). All rights reserved.
// Use of this source code is governed by the MIT License
// that can be found in the LICENSE file.

// Package streaming implements flawless SSE (Server-Sent Events) streaming support.
// It ensures the word-by-word streaming experience works perfectly across all providers.
package streaming

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

	"github.com/smilespoon/nexusguard-ai/pkg/providers"
)

// SSEWriter handles Server-Sent Events output.
type SSEWriter struct {
	writer  http.ResponseWriter
	flusher http.Flusher
	mu      sync.Mutex
}

// NewSSEWriter creates an SSE writer from an HTTP response writer.
func NewSSEWriter(w http.ResponseWriter) (*SSEWriter, error) {
	flusher, ok := w.(http.Flusher)
	if !ok {
		return nil, fmt.Errorf("streaming not supported")
	}

	// Set SSE headers
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.Header().Set("X-Accel-Buffering", "no")
	w.WriteHeader(http.StatusOK)
	flusher.Flush()

	return &SSEWriter{
		writer:  w,
		flusher: flusher,
	}, nil
}

// WriteEvent sends a single SSE event.
func (s *SSEWriter) WriteEvent(data string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	_, err := fmt.Fprintf(s.writer, "data: %s\n\n", data)
	if err != nil {
		return err
	}
	s.flusher.Flush()
	return nil
}

// WriteDone sends the completion event.
func (s *SSEWriter) WriteDone() error {
	return s.WriteEvent("[DONE]")
}

// WriteError sends an error event.
func (s *SSEWriter) WriteError(errMsg string) error {
	errData := map[string]string{"error": errMsg}
	data, _ := json.Marshal(errData)
	return s.WriteEvent(string(data))
}

// ProxyOpenAIStream proxies an OpenAI-format SSE stream.
func ProxyOpenAIStream(ctx context.Context, upstream io.ReadCloser, w http.ResponseWriter) error {
	defer upstream.Close()

	sse, err := NewSSEWriter(w)
	if err != nil {
		return err
	}

	reader := bufio.NewReader(upstream)
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}

		line, err := reader.ReadString('\n')
		if err != nil {
			if err == io.EOF {
				sse.WriteDone()
				return nil
			}
			return err
		}

		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}

		if strings.HasPrefix(line, "data: ") {
			data := strings.TrimPrefix(line, "data: ")
			if data == "[DONE]" {
				sse.WriteDone()
				return nil
			}

			if err := sse.WriteEvent(data); err != nil {
				return err
			}
		}
	}
}

// TransformToOpenAI converts any provider stream to OpenAI format.
func TransformToOpenAI(ch <-chan providers.StreamChunk, w http.ResponseWriter) error {
	sse, err := NewSSEWriter(w)
	if err != nil {
		return err
	}

	for chunk := range ch {
		if chunk.Done {
			return sse.WriteDone()
		}

		// Format as OpenAI-compatible chunk
		openAIChunk := map[string]interface{}{
			"object": "chat.completion.chunk",
			"choices": []map[string]interface{}{
				{
					"index": 0,
					"delta": map[string]string{
						"content": chunk.Content,
					},
					"finish_reason": nil,
				},
			},
		}

		data, _ := json.Marshal(openAIChunk)
		if err := sse.WriteEvent(string(data)); err != nil {
			return err
		}
	}

	return sse.WriteDone()
}

// StreamAggregator collects stream chunks into a complete response.
type StreamAggregator struct {
	content  strings.Builder
	done     bool
	provider string
}

// NewAggregator creates a stream aggregator.
func NewAggregator() *StreamAggregator {
	return &StreamAggregator{}
}

// AddChunk processes a stream chunk.
func (a *StreamAggregator) AddChunk(chunk providers.StreamChunk) {
	if chunk.Done {
		a.done = true
		return
	}
	a.content.WriteString(chunk.Content)
	a.provider = chunk.Provider
}

// Content returns the accumulated content.
func (a *StreamAggregator) Content() string {
	return a.content.String()
}

// Done returns whether the stream is complete.
func (a *StreamAggregator) Done() bool {
	return a.done
}

// Provider returns the provider name.
func (a *StreamAggregator) Provider() string {
	return a.provider
}

// isStreamingRequest detects if a request wants streaming.
func IsStreamingRequest(body []byte) bool {
	var req struct {
		Stream bool `json:"stream"`
	}
	json.Unmarshal(body, &req)
	return req.Stream
}

// CopyHeaders copies relevant headers from upstream to downstream.
func CopyHeaders(dst, src http.Header) {
	for _, key := range []string{
		"Content-Type",
		"Cache-Control",
		"X-Request-Id",
	} {
		if v := src.Get(key); v != "" {
			dst.Set(key, v)
		}
	}
}

// BufferPool is a pool of reusable buffers for streaming.
type BufferPool struct {
	pool sync.Pool
}

// NewBufferPool creates a buffer pool.
func NewBufferPool() *BufferPool {
	return &BufferPool{
		pool: sync.Pool{
			New: func() interface{} {
				return new(bytes.Buffer)
			},
		},
	}
}

// Get retrieves a buffer from the pool.
func (p *BufferPool) Get() *bytes.Buffer {
	return p.pool.Get().(*bytes.Buffer)
}

// Put returns a buffer to the pool.
func (p *BufferPool) Put(b *bytes.Buffer) {
	b.Reset()
	p.pool.Put(b)
}


