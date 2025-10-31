package docker

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/docker/docker/api/types"
	"github.com/docker/docker/api/types/container"
	"github.com/docker/docker/client"
	"github.com/docker/docker/pkg/archive"
	"github.com/docker/go-connections/nat"
)

// ContainerInfo captures minimal runtime details about a started container.
type ContainerInfo struct {
	ID          string
	PortBinding nat.PortMap
}

// BuildOutputCallback is invoked with incremental build messages.
type BuildOutputCallback func(string)

// BuildImage creates a Docker image from the provided directory using the default Dockerfile.
func (c *Client) BuildImage(ctx context.Context, dir, tag string, buildArgs map[string]*string, onOutput BuildOutputCallback) error {
	if c.inner == nil {
		return fmt.Errorf("docker client not initialized")
	}
	if dir == "" {
		return fmt.Errorf("build directory cannot be empty")
	}
	if tag == "" {
		return fmt.Errorf("image tag cannot be empty")
	}
	buildCtx, err := archive.TarWithOptions(dir, &archive.TarOptions{})
	if err != nil {
		return fmt.Errorf("create build context: %w", err)
	}
	defer buildCtx.Close()

	opts := types.ImageBuildOptions{
		Tags:        []string{tag},
		Remove:      true,
		ForceRemove: true,
		BuildArgs:   buildArgs,
	}
	resp, err := c.inner.ImageBuild(ctx, buildCtx, opts)
	if err != nil {
		return fmt.Errorf("docker image build: %w", err)
	}
	defer resp.Body.Close()
	decoder := json.NewDecoder(resp.Body)
	for {
		var msg imageBuildMessage
		if err := decoder.Decode(&msg); err != nil {
			if err == io.EOF {
				break
			}
			return fmt.Errorf("decode build output: %w", err)
		}

		if errMsg := msg.errorMessage(); errMsg != "" {
			return fmt.Errorf("docker image build: %s", errMsg)
		}

		line := msg.render()
		if line != "" && onOutput != nil {
			onOutput(line)
		}
	}
	return nil
}

// RemoveContainer removes an existing container if it exists.
func (c *Client) RemoveContainer(ctx context.Context, name string) error {
	if strings.TrimSpace(name) == "" {
		return fmt.Errorf("container name cannot be empty")
	}
	if err := c.inner.ContainerRemove(ctx, name, container.RemoveOptions{Force: true, RemoveVolumes: true}); err != nil {
		if client.IsErrNotFound(err) {
			return nil
		}
		return fmt.Errorf("remove container: %w", err)
	}
	return nil
}

// WaitForStop blocks until the container stops and returns the exit code.
func (c *Client) WaitForStop(ctx context.Context, containerID string) (int64, error) {
	if strings.TrimSpace(containerID) == "" {
		return 0, fmt.Errorf("container id cannot be empty")
	}
	statusCh, errCh := c.inner.ContainerWait(ctx, containerID, container.WaitConditionNotRunning)
	for {
		select {
		case err := <-errCh:
			if err == nil {
				continue
			}
			if client.IsErrNotFound(err) {
				return 0, nil
			}
			return 0, fmt.Errorf("wait for container stop: %w", err)
		case status := <-statusCh:
			return status.StatusCode, nil
		case <-ctx.Done():
			return 0, ctx.Err()
		}
	}
}

type imageBuildMessage struct {
	Stream         string                 `json:"stream"`
	Status         string                 `json:"status"`
	ID             string                 `json:"id"`
	Progress       string                 `json:"progress"`
	ProgressDetail progressDetail         `json:"progressDetail"`
	Error          string                 `json:"error"`
	ErrorDetail    imageBuildErrorDetail  `json:"errorDetail"`
	Aux            map[string]interface{} `json:"aux"`
}

type progressDetail struct {
	Current int64 `json:"current"`
	Total   int64 `json:"total"`
}

type imageBuildErrorDetail struct {
	Message string `json:"message"`
}

func (m imageBuildMessage) errorMessage() string {
	if strings.TrimSpace(m.Error) != "" {
		return strings.TrimSpace(m.Error)
	}
	if strings.TrimSpace(m.ErrorDetail.Message) != "" {
		return strings.TrimSpace(m.ErrorDetail.Message)
	}
	return ""
}

func (m imageBuildMessage) render() string {
	if m.Stream != "" {
		return m.Stream
	}
	if m.Status != "" {
		parts := make([]string, 0, 4)
		if strings.TrimSpace(m.ID) != "" {
			parts = append(parts, strings.TrimSpace(m.ID))
		}
		parts = append(parts, strings.TrimSpace(m.Status))
		progress := strings.TrimSpace(m.Progress)
		if progress == "" && (m.ProgressDetail.Current > 0 || m.ProgressDetail.Total > 0) {
			if m.ProgressDetail.Total > 0 {
				progress = fmt.Sprintf("%d/%d", m.ProgressDetail.Current, m.ProgressDetail.Total)
			} else {
				progress = fmt.Sprintf("%d", m.ProgressDetail.Current)
			}
		}
		if progress != "" {
			parts = append(parts, progress)
		}
		return strings.TrimSpace(strings.Join(parts, " "))
	}
	if len(m.Aux) > 0 {
		if id, ok := m.Aux["ID"]; ok {
			return fmt.Sprintf("image id: %v", id)
		}
		if digest, ok := m.Aux["Digest"]; ok {
			return fmt.Sprintf("digest: %v", digest)
		}
	}
	return ""
}

// RunContainer creates and starts a container exposing the provided port mappings.
func (c *Client) RunContainer(ctx context.Context, name, image string, cmd []string, env []string, ports nat.PortMap) (ContainerInfo, error) {
	if strings.TrimSpace(name) == "" {
		return ContainerInfo{}, fmt.Errorf("container name cannot be empty")
	}
	if strings.TrimSpace(image) == "" {
		return ContainerInfo{}, fmt.Errorf("image name cannot be empty")
	}

	config := &container.Config{
		Image:        image,
		Cmd:          cmd,
		Env:          env,
		ExposedPorts: map[nat.Port]struct{}{},
	}
	for p := range ports {
		config.ExposedPorts[p] = struct{}{}
	}

	hostCfg := &container.HostConfig{
		PortBindings: ports,
		RestartPolicy: container.RestartPolicy{
			Name: "always",
		},
	}

	r, err := c.inner.ContainerCreate(ctx, config, hostCfg, nil, nil, name)
	if err != nil {
		return ContainerInfo{}, fmt.Errorf("container create: %w", err)
	}

	if err := c.inner.ContainerStart(ctx, r.ID, container.StartOptions{}); err != nil {
		return ContainerInfo{}, fmt.Errorf("container start: %w", err)
	}

	var inspect types.ContainerJSON
	for attempt := 0; attempt < 10; attempt++ {
		inspect, err = c.inner.ContainerInspect(ctx, r.ID)
		if err != nil {
			return ContainerInfo{}, fmt.Errorf("container inspect: %w", err)
		}
		if hasHostPort(inspect.NetworkSettings) {
			break
		}
		if attempt == 9 {
			break
		}
		select {
		case <-ctx.Done():
			return ContainerInfo{}, fmt.Errorf("wait for host port: %w", ctx.Err())
		case <-time.After(200 * time.Millisecond):
		}
	}

	portsBinding := nat.PortMap{}
	if inspect.NetworkSettings != nil && inspect.NetworkSettings.Ports != nil {
		portsBinding = inspect.NetworkSettings.Ports
	}

	return ContainerInfo{ID: r.ID, PortBinding: portsBinding}, nil
}

func hasHostPort(settings *types.NetworkSettings) bool {
	if settings == nil || settings.Ports == nil {
		return false
	}
	for _, bindings := range settings.Ports {
		for _, binding := range bindings {
			if strings.TrimSpace(binding.HostPort) != "" {
				return true
			}
		}
	}
	return false
}
