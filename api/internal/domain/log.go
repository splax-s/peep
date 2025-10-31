package domain

import "time"

// ProjectLog represents a log line emitted by build/runtime processes.
type ProjectLog struct {
	ID        int64
	ProjectID string
	Source    string
	Level     string
	Message   string
	Metadata  []byte
	CreatedAt time.Time
}
