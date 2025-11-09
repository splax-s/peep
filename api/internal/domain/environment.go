package domain

import "time"

// Environment represents a deployment context such as dev/staging/prod.
type Environment struct {
	ID              string
	ProjectID       string
	Slug            string
	Name            string
	EnvironmentType string
	Protected       bool
	Position        int
	CreatedAt       time.Time
	UpdatedAt       time.Time
}

// EnvironmentVersion captures a versioned snapshot of environment variables.
type EnvironmentVersion struct {
	ID            string
	EnvironmentID string
	Version       int
	Description   string
	CreatedBy     *string
	CreatedAt     time.Time
}

// EnvironmentVariable stores an encrypted key/value pair belonging to a version.
type EnvironmentVariable struct {
	VersionID string
	Key       string
	Value     []byte
	Checksum  *string
	CreatedAt time.Time
}

// EnvironmentAudit tracks changes applied to environments and versions.
type EnvironmentAudit struct {
	ID            int64
	ProjectID     string
	EnvironmentID *string
	VersionID     *string
	ActorID       *string
	Action        string
	Metadata      []byte
	CreatedAt     time.Time
}
