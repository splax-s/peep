package docker

import (
	"context"
	"fmt"

	"github.com/docker/docker/api/types"
	"github.com/docker/docker/client"
)

// Client wraps the Docker SDK client.
type Client struct {
	inner *client.Client
}

// New creates a new Docker client using environment defaults.
func New(host string) (*Client, error) {
	opts := []client.Opt{client.FromEnv, client.WithAPIVersionNegotiation()}
	if host != "" {
		opts = append(opts, client.WithHost(host))
	}
	inner, err := client.NewClientWithOpts(opts...)
	if err != nil {
		return nil, fmt.Errorf("create docker client: %w", err)
	}
	return &Client{inner: inner}, nil
}

// Ping validates connectivity to the Docker daemon.
func (c *Client) Ping(ctx context.Context) error {
	if c == nil || c.inner == nil {
		return fmt.Errorf("docker client not initialized")
	}
	var ping types.Ping
	ping, err := c.inner.Ping(ctx)
	if err != nil {
		return fmt.Errorf("docker ping: %w", err)
	}
	if ping.APIVersion == "" {
		return fmt.Errorf("docker ping returned empty API version")
	}
	return nil
}

// Inner exposes the underlying docker client for advanced operations.
func (c *Client) Inner() *client.Client {
	return c.inner
}

// Close releases resources held by the Docker client.
func (c *Client) Close() error {
	if c.inner == nil {
		return nil
	}
	return c.inner.Close()
}
