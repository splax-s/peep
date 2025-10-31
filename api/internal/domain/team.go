package domain

import "time"

// Team represents a collaborative group with resource quotas.
type Team struct {
	ID             string
	Name           string
	OwnerID        string
	MaxProjects    int
	MaxContainers  int
	StorageLimitMB int
	CreatedAt      time.Time
}

// TeamMember links a user to a team with a role.
type TeamMember struct {
	TeamID    string
	UserID    string
	Role      string
	CreatedAt time.Time
}
