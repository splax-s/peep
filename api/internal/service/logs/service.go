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
	payload := map[string]any{
		"project_id": entry.ProjectID,
		"source":     entry.Source,
		"level":      entry.Level,
		"message":    entry.Message,
		"metadata":   entry.Metadata,
		"created_at": entry.CreatedAt.Format(time.RFC3339Nano),
	}
	data, err := json.Marshal(payload)
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
