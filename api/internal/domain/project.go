package domain

import "time"

// Project describes a deployable unit.
type Project struct {
	ID           string
	TeamID       string
	Name         string
	RepoURL      string
	Type         string
	BuildCommand string
	RunCommand   string
	CreatedAt    time.Time
}

// ProjectEnvVar stores encrypted environment variables.
type ProjectEnvVar struct {
	ProjectID string
	Key       string
	Value     []byte
	CreatedAt time.Time
}

// ProjectContainer tracks running container endpoints for ingress wiring.
type ProjectContainer struct {
	ID            string
	ProjectID     string
	DeploymentID  string
	ContainerID   string
	Status        string
	HostIP        string
	HostPort      int
	CPUPercent    *float64
	MemoryBytes   *int64
	UptimeSeconds *int64
	CreatedAt     time.Time
	UpdatedAt     time.Time
}
