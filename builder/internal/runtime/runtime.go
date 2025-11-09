package runtime

import (
	"context"
	"time"
)

// Request describes a runtime deployment request against the cluster.
type Request struct {
	DeploymentID string
	ProjectID    string
	Image        string
	Command      []string
	Port         int
	Timeout      time.Duration
}

// Deployment contains resolved runtime metadata once ready.
type Deployment struct {
	DeploymentName string
	ServiceName    string
	PodName        string
	Host           string
	Port           int
	StartedAt      time.Time
}

// PodStatus provides a snapshot of runtime pod state and resource signals.
type PodStatus struct {
	Phase       string
	Reason      string
	Message     string
	Ready       bool
	StartedAt   *time.Time
	ContainerID string
	CPUPercent  float64
	MemoryBytes int64
}

// Manager provisions and monitors runtime workloads.
type Manager interface {
	Deploy(ctx context.Context, req Request) (Deployment, error)
	Cancel(ctx context.Context, deploymentID string) error
	PodStatus(ctx context.Context, deploymentID string) (PodStatus, error)
}
