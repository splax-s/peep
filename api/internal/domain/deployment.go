package domain

import (
	"encoding/json"
	"time"
)

// Deployment captures a single deployment attempt.
type Deployment struct {
	ID                   string
	ProjectID            string
	EnvironmentID        string
	EnvironmentVersionID string
	RolledBackFrom       *string
	CommitSHA            string
	Status               string
	Stage                string
	Message              string
	URL                  string
	Error                string
	Metadata             json.RawMessage
	StartedAt            time.Time
	CompletedAt          *time.Time
	UpdatedAt            time.Time
}

// DeploymentStatusUpdate captures mutable fields for a deployment.
type DeploymentStatusUpdate struct {
	DeploymentID string
	Status       string
	Stage        string
	Message      string
	URL          string
	Error        string
	Metadata     json.RawMessage
	CompletedAt  *time.Time
}
