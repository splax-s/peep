package ingress

import (
	"context"
	"fmt"
	"strings"

	"github.com/docker/docker/client"
	"github.com/docker/docker/errdefs"
)

// dockerReloader triggers nginx reloads by signalling a Docker container.
type dockerReloader struct {
	client    *client.Client
	container string
}

func newDockerReloader(container string) (*dockerReloader, error) {
	container = strings.TrimSpace(container)
	if container == "" {
		return nil, fmt.Errorf("container name required")
	}
	cli, err := client.NewClientWithOpts(client.FromEnv, client.WithAPIVersionNegotiation())
	if err != nil {
		return nil, err
	}
	return &dockerReloader{client: cli, container: container}, nil
}

func (r *dockerReloader) Reload(ctx context.Context) error {
	if err := r.client.ContainerKill(ctx, r.container, "HUP"); err != nil {
		if errdefs.IsNotFound(err) {
			return fmt.Errorf("nginx container %s not found", r.container)
		}
		return err
	}
	return nil
}

func (r *dockerReloader) Close() error {
	return r.client.Close()
}
