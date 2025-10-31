package team

import (
	"context"
	"errors"
	"time"

	"log/slog"

	"github.com/google/uuid"

	"github.com/splax/localvercel/api/internal/domain"
	"github.com/splax/localvercel/api/internal/repository"
)

// Limits captures configurable resource quotas.
type Limits struct {
	MaxProjects    int `json:"max_projects"`
	MaxContainers  int `json:"max_containers"`
	StorageLimitMB int `json:"storage_limit_mb"`
}

// Service handles team workflows.
type Service struct {
	repo   repository.TeamRepository
	logger *slog.Logger
}

// New constructs a Service with default logging.
func New(repo repository.TeamRepository, logger *slog.Logger) Service {
	return Service{repo: repo, logger: logger}
}

var (
	errInvalidTeamName = errors.New("team name is required")
	errProjectQuota    = errors.New("team project quota exceeded")
)

// Create registers a team for the owner.
func (s Service) Create(ctx context.Context, ownerID, name string, limits Limits) (*domain.Team, error) {
	if name == "" {
		return nil, errInvalidTeamName
	}
	if limits.MaxProjects == 0 {
		limits.MaxProjects = 5
	}
	if limits.MaxContainers == 0 {
		limits.MaxContainers = 10
	}
	if limits.StorageLimitMB == 0 {
		limits.StorageLimitMB = 10240
	}
	team := &domain.Team{
		ID:             uuid.NewString(),
		Name:           name,
		OwnerID:        ownerID,
		MaxProjects:    limits.MaxProjects,
		MaxContainers:  limits.MaxContainers,
		StorageLimitMB: limits.StorageLimitMB,
		CreatedAt:      time.Now().UTC(),
	}
	if err := s.repo.CreateTeam(ctx, team); err != nil {
		return nil, err
	}
	member := &domain.TeamMember{
		TeamID:    team.ID,
		UserID:    ownerID,
		Role:      "owner",
		CreatedAt: time.Now().UTC(),
	}
	if err := s.repo.UpsertMember(ctx, member); err != nil {
		return nil, err
	}
	s.logger.Info("team created", "team_id", team.ID, "owner_id", ownerID)
	return team, nil
}

// EnsureProjectQuota verifies team capacity before adding new project.
func (s Service) EnsureProjectQuota(ctx context.Context, teamID string) error {
	count, err := s.repo.CountProjects(ctx, teamID)
	if err != nil {
		return err
	}
	team, err := s.repo.GetTeamByID(ctx, teamID)
	if err != nil {
		return err
	}
	if count >= team.MaxProjects {
		return errProjectQuota
	}
	return nil
}

// UpsertMember adds or updates membership.
func (s Service) UpsertMember(ctx context.Context, teamID, userID, role string) error {
	member := &domain.TeamMember{
		TeamID:    teamID,
		UserID:    userID,
		Role:      role,
		CreatedAt: time.Now().UTC(),
	}
	return s.repo.UpsertMember(ctx, member)
}
