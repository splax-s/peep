package logs

import (
	"context"
	"encoding/json"
	"time"

	"log/slog"

	"github.com/splax/localvercel/api/internal/domain"
	"github.com/splax/localvercel/api/internal/repository"
	"github.com/splax/localvercel/api/internal/ws"
)

// Service handles log persistence and streaming.
type Service struct {
	repo   repository.LogRepository
	hub    *ws.Hub
	logger *slog.Logger
}

// New constructs a log service.
func New(repo repository.LogRepository, hub *ws.Hub, logger *slog.Logger) Service {
	return Service{repo: repo, hub: hub, logger: logger}
}

// Append stores and broadcasts a log entry.
func (s Service) Append(ctx context.Context, entry domain.ProjectLog) error {
	entry.CreatedAt = entry.CreatedAt.UTC()
	if err := s.repo.AppendLog(ctx, entry); err != nil {
		return err
	}
	s.broadcast(entry)
	return nil
}

// List returns logs for a project.
func (s Service) List(ctx context.Context, projectID string, limit, offset int) ([]domain.ProjectLog, error) {
	return s.repo.ListLogsByProject(ctx, projectID, limit, offset)
}

// BroadcastPublic sends log entry to websocket clients.
func (s Service) broadcast(entry domain.ProjectLog) {
	data, err := MarshalEntry(entry)
	if err != nil {
		s.logger.Warn("failed to marshal log payload", "error", err)
		return
	}
	s.hub.Broadcast(entry.ProjectID, data)
}

// Hub returns the websocket hub (useful for HTTP handlers).
func (s Service) Hub() *ws.Hub {
	return s.hub
}

// MarshalEntry formats a project log for streaming payloads.
func MarshalEntry(entry domain.ProjectLog) ([]byte, error) {
	var metadata any
	if len(entry.Metadata) > 0 {
		metadata = json.RawMessage(entry.Metadata)
	} else {
		metadata = nil
	}
	payload := map[string]any{
		"project_id": entry.ProjectID,
		"source":     entry.Source,
		"level":      entry.Level,
		"message":    entry.Message,
		"metadata":   metadata,
		"created_at": entry.CreatedAt.Format(time.RFC3339Nano),
		"id":         entry.ID,
	}
	return json.Marshal(payload)
}
