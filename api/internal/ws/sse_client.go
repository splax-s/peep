package ws

import (
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"sync"
	"time"
)

// SSEClient streams Server-Sent Events over an HTTP response writer.
type SSEClient struct {
	mu      sync.Mutex
	writer  io.Writer
	flusher http.Flusher
	log     *slog.Logger
	closed  bool
	last    time.Time
}

// NewSSEClient builds an SSE client instance.
func NewSSEClient(writer io.Writer, flusher http.Flusher, logger *slog.Logger) *SSEClient {
	return &SSEClient{writer: writer, flusher: flusher, log: logger, last: time.Now().UTC()}
}

// Send emits a data event to the SSE stream.
func (c *SSEClient) Send(payload []byte) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.closed {
		return io.EOF
	}
	if _, err := fmt.Fprintf(c.writer, "data: %s\n\n", payload); err != nil {
		c.closed = true
		c.log.Warn("sse send failed", "error", err)
		return err
	}
	c.flusher.Flush()
	c.last = time.Now().UTC()
	return nil
}

// Heartbeat emits a comment frame to keep the connection alive.
func (c *SSEClient) Heartbeat() error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.closed {
		return io.EOF
	}
	if _, err := fmt.Fprint(c.writer, ": ping\n\n"); err != nil {
		c.closed = true
		c.log.Warn("sse heartbeat failed", "error", err)
		return err
	}
	c.flusher.Flush()
	c.last = time.Now().UTC()
	return nil
}

// Close marks the stream as closed.
func (c *SSEClient) Close() {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.closed = true
}

// LastActivity reports the timestamp of the most recent successful write.
func (c *SSEClient) LastActivity() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.last
}
