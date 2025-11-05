package ws

import (
	"log/slog"

	"github.com/gorilla/websocket"
)

// Client represents a websocket client connection.
type Client struct {
	conn *websocket.Conn
	log  *slog.Logger
}

// NewClient constructs a client wrapper.
func NewClient(conn *websocket.Conn, logger *slog.Logger) *Client {
	return &Client{conn: conn, log: logger}
}

// Send writes a message to the websocket connection.
func (c *Client) Send(payload []byte) error {
	if err := c.conn.WriteMessage(websocket.TextMessage, payload); err != nil {
		c.log.Warn("websocket send failed", "error", err)
		_ = c.conn.Close()
		return err
	}
	return nil
}

// Close terminates the connection.
func (c *Client) Close() {
	_ = c.conn.Close()
}
