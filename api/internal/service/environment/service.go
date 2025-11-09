package environment

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"strings"
	"time"

	"log/slog"

	"github.com/google/uuid"

	"github.com/splax/localvercel/api/internal/domain"
	"github.com/splax/localvercel/api/internal/repository"
	"github.com/splax/localvercel/pkg/config"
	"github.com/splax/localvercel/pkg/crypto"
)

var slugExpr = regexp.MustCompile(`[^a-z0-9-]+`)

// Service coordinates environment hierarchy operations.
type Service struct {
	envs     repository.EnvironmentRepository
	projects repository.ProjectRepository
	logger   *slog.Logger
	cfg      config.APIConfig
}

// New constructs an environment service.
func New(envs repository.EnvironmentRepository, projects repository.ProjectRepository, logger *slog.Logger, cfg config.APIConfig) Service {
	return Service{envs: envs, projects: projects, logger: logger, cfg: cfg}
}

// CreateEnvironmentInput captures attributes for a new environment.
type CreateEnvironmentInput struct {
	ProjectID       string
	Name            string
	Slug            string
	EnvironmentType string
	Protected       bool
	Position        int
	ActorID         *string
}

// UpdateEnvironmentInput captures mutable environment fields.
type UpdateEnvironmentInput struct {
	EnvironmentID   string
	Name            *string
	Slug            *string
	EnvironmentType *string
	Protected       *bool
	Position        *int
}

// VariableInput holds plaintext environment variable values.
type VariableInput struct {
	Key   string
	Value string
}

// CreateVersionInput models a new environment version request.
type CreateVersionInput struct {
	EnvironmentID string
	Description   string
	Variables     []VariableInput
	ActorID       *string
}

// Variable represents a decrypted environment variable.
type Variable struct {
	Key      string `json:"key"`
	Value    string `json:"value"`
	Checksum string `json:"checksum,omitempty"`
}

// VersionDetails bundles version metadata with decrypted variables.
type VersionDetails struct {
	Version   domain.EnvironmentVersion `json:"version"`
	Variables []Variable                `json:"variables"`
}

// EnvironmentDetails summarizes an environment with its latest version.
type EnvironmentDetails struct {
	Environment   domain.Environment `json:"environment"`
	LatestVersion *VersionDetails    `json:"latest_version,omitempty"`
}

var (
	errProjectIDRequired     = fmt.Errorf("%w: project id required", repository.ErrInvalidArgument)
	errEnvironmentName       = fmt.Errorf("%w: name required", repository.ErrInvalidArgument)
	errEnvironmentIDRequired = fmt.Errorf("%w: environment id required", repository.ErrInvalidArgument)
	errEnvironmentIDInvalid  = fmt.Errorf("%w: environment id invalid", repository.ErrInvalidArgument)
	errVersionIDRequired     = fmt.Errorf("%w: version id required", repository.ErrInvalidArgument)
	errVariableKeyRequired   = fmt.Errorf("%w: variable key required", repository.ErrInvalidArgument)
)

// List returns all environments for a project with their latest snapshot.
func (s Service) List(ctx context.Context, projectID string) ([]EnvironmentDetails, error) {
	projectID = strings.TrimSpace(projectID)
	if projectID == "" {
		return nil, errProjectIDRequired
	}
	envs, err := s.envs.ListEnvironmentsByProject(ctx, projectID)
	if err != nil {
		return nil, err
	}
	results := make([]EnvironmentDetails, 0, len(envs))
	for _, env := range envs {
		detail := EnvironmentDetails{Environment: env}
		latest, vars, err := s.envs.GetLatestEnvironmentVersion(ctx, env.ID)
		if err != nil {
			if errors.Is(err, repository.ErrNotFound) {
				results = append(results, detail)
				continue
			}
			return nil, err
		}
		if latest != nil {
			decoded, err := s.decryptVariables(vars)
			if err != nil {
				return nil, err
			}
			copy := *latest
			detail.LatestVersion = &VersionDetails{Version: copy, Variables: decoded}
		}
		results = append(results, detail)
	}
	return results, nil
}

// Detail returns information for a single environment including its latest version.
func (s Service) Detail(ctx context.Context, environmentID string) (*EnvironmentDetails, error) {
	environmentID = strings.TrimSpace(environmentID)
	if environmentID == "" {
		return nil, errEnvironmentIDRequired
	}
	env, err := s.envs.GetEnvironmentByID(ctx, environmentID)
	if err != nil {
		return nil, err
	}
	detail := EnvironmentDetails{Environment: *env}
	latest, vars, err := s.envs.GetLatestEnvironmentVersion(ctx, env.ID)
	if err != nil {
		if errors.Is(err, repository.ErrNotFound) {
			return &detail, nil
		}
		return nil, err
	}
	if latest == nil {
		return &detail, nil
	}
	decoded, err := s.decryptVariables(vars)
	if err != nil {
		return nil, err
	}
	copy := *latest
	detail.LatestVersion = &VersionDetails{Version: copy, Variables: decoded}
	return &detail, nil
}

// Create registers a new environment and seeds an initial version.
func (s Service) Create(ctx context.Context, input CreateEnvironmentInput) (*domain.Environment, error) {
	input.ProjectID = strings.TrimSpace(input.ProjectID)
	if input.ProjectID == "" {
		return nil, errProjectIDRequired
	}
	if strings.TrimSpace(input.Name) == "" {
		return nil, errEnvironmentName
	}
	if _, err := s.projects.GetProjectByID(ctx, input.ProjectID); err != nil {
		return nil, err
	}
	existing, err := s.envs.ListEnvironmentsByProject(ctx, input.ProjectID)
	if err != nil {
		return nil, err
	}
	slug := normalizeSlug(input.Slug)
	if slug == "" {
		slug = normalizeSlug(input.Name)
	}
	if slug == "" {
		slug = uuid.NewString()
	}
	for _, env := range existing {
		if env.Slug == slug {
			return nil, fmt.Errorf("%w: slug already exists", repository.ErrInvalidArgument)
		}
	}
	position := input.Position
	if position <= 0 {
		position = len(existing) + 1
	}
	envType := strings.ToLower(strings.TrimSpace(input.EnvironmentType))
	if envType == "" {
		envType = "custom"
	}
	environment := &domain.Environment{
		ID:              uuid.NewString(),
		ProjectID:       input.ProjectID,
		Slug:            slug,
		Name:            strings.TrimSpace(input.Name),
		EnvironmentType: envType,
		Protected:       input.Protected,
		Position:        position,
		CreatedAt:       time.Now().UTC(),
	}
	if err := s.envs.CreateEnvironment(ctx, environment); err != nil {
		return nil, err
	}
	if err := s.seedInitialVersion(ctx, environment, input.ActorID); err != nil {
		return nil, err
	}
	return environment, nil
}

// Update mutates environment metadata.
func (s Service) Update(ctx context.Context, input UpdateEnvironmentInput) (*domain.Environment, error) {
	input.EnvironmentID = strings.TrimSpace(input.EnvironmentID)
	if input.EnvironmentID == "" {
		return nil, errEnvironmentIDRequired
	}
	environment, err := s.envs.GetEnvironmentByID(ctx, input.EnvironmentID)
	if err != nil {
		return nil, err
	}
	if input.Name != nil {
		trimmed := strings.TrimSpace(*input.Name)
		if trimmed == "" {
			return nil, errEnvironmentName
		}
		environment.Name = trimmed
	}
	if input.Slug != nil {
		slug := normalizeSlug(*input.Slug)
		if slug == "" {
			return nil, fmt.Errorf("%w: invalid slug", repository.ErrInvalidArgument)
		}
		environment.Slug = slug
	}
	if input.EnvironmentType != nil {
		environment.EnvironmentType = strings.ToLower(strings.TrimSpace(*input.EnvironmentType))
	}
	if input.Protected != nil {
		environment.Protected = *input.Protected
	}
	if input.Position != nil {
		if *input.Position <= 0 {
			return nil, fmt.Errorf("%w: position must be positive", repository.ErrInvalidArgument)
		}
		environment.Position = *input.Position
	}
	if err := s.envs.UpdateEnvironment(ctx, environment); err != nil {
		return nil, err
	}
	return environment, nil
}

// CreateVersion publishes a new immutable snapshot for the environment.
func (s Service) CreateVersion(ctx context.Context, input CreateVersionInput) (*VersionDetails, error) {
	input.EnvironmentID = strings.TrimSpace(input.EnvironmentID)
	if input.EnvironmentID == "" {
		return nil, errEnvironmentIDRequired
	}
	environment, err := s.envs.GetEnvironmentByID(ctx, input.EnvironmentID)
	if err != nil {
		return nil, err
	}
	latest, _, err := s.envs.GetLatestEnvironmentVersion(ctx, environment.ID)
	nextVersion := 1
	if err == nil && latest != nil {
		nextVersion = latest.Version + 1
	} else if err != nil && !errors.Is(err, repository.ErrNotFound) {
		return nil, err
	}
	variables, err := s.prepareVariables(input.Variables)
	if err != nil {
		return nil, err
	}
	version := &domain.EnvironmentVersion{
		ID:            uuid.NewString(),
		EnvironmentID: environment.ID,
		Version:       nextVersion,
		Description:   strings.TrimSpace(input.Description),
		CreatedAt:     time.Now().UTC(),
	}
	if input.ActorID != nil {
		actor := strings.TrimSpace(*input.ActorID)
		if actor != "" {
			version.CreatedBy = &actor
		}
	}
	if err := s.envs.CreateEnvironmentVersion(ctx, version, variables); err != nil {
		return nil, err
	}
	if err := s.recordAudit(ctx, environment.ProjectID, environment.ID, version.ID, input.ActorID, "version_created", map[string]any{
		"version":        version.Version,
		"variable_count": len(variables),
	}); err != nil {
		return nil, err
	}
	decrypted, err := s.decryptVariables(variables)
	if err != nil {
		return nil, err
	}
	return &VersionDetails{Version: *version, Variables: decrypted}, nil
}

// GetVersion returns decrypted data for a specific version.
func (s Service) GetVersion(ctx context.Context, versionID string) (*VersionDetails, error) {
	versionID = strings.TrimSpace(versionID)
	if versionID == "" {
		return nil, errVersionIDRequired
	}
	version, vars, err := s.envs.GetEnvironmentVersion(ctx, versionID)
	if err != nil {
		return nil, err
	}
	decrypted, err := s.decryptVariables(vars)
	if err != nil {
		return nil, err
	}
	return &VersionDetails{Version: *version, Variables: decrypted}, nil
}

// ListVersions enumerates versions for an environment without decrypting values.
func (s Service) ListVersions(ctx context.Context, environmentID string, limit int) ([]domain.EnvironmentVersion, error) {
	environmentID = strings.TrimSpace(environmentID)
	if environmentID == "" {
		return nil, errEnvironmentIDRequired
	}
	return s.envs.ListEnvironmentVersions(ctx, environmentID, limit)
}

// GetEnvironment returns the underlying environment without version data.
func (s Service) GetEnvironment(ctx context.Context, environmentID string) (*domain.Environment, error) {
	environmentID = strings.TrimSpace(environmentID)
	if environmentID == "" {
		return nil, errEnvironmentIDRequired
	}
	return s.envs.GetEnvironmentByID(ctx, environmentID)
}

// ListAudits returns recent audit entries for the environment hierarchy.
func (s Service) ListAudits(ctx context.Context, projectID, environmentID string, limit int) ([]domain.EnvironmentAudit, error) {
	projectID = strings.TrimSpace(projectID)
	if projectID == "" {
		return nil, errProjectIDRequired
	}
	environmentID = strings.TrimSpace(environmentID)
	if environmentID != "" {
		if _, err := uuid.Parse(environmentID); err != nil {
			return nil, errEnvironmentIDInvalid
		}
	}
	return s.envs.ListEnvironmentAudits(ctx, projectID, environmentID, limit)
}

func (s Service) seedInitialVersion(ctx context.Context, environment *domain.Environment, actorID *string) error {
	if environment == nil {
		return errors.New("environment: missing entity")
	}
	if err := s.recordAudit(ctx, environment.ProjectID, environment.ID, "", actorID, "environment_created", map[string]any{
		"slug": environment.Slug,
	}); err != nil {
		return err
	}
	if _, _, err := s.envs.GetLatestEnvironmentVersion(ctx, environment.ID); err == nil {
		return nil
	} else if !errors.Is(err, repository.ErrNotFound) {
		return err
	}
	version := &domain.EnvironmentVersion{
		ID:            uuid.NewString(),
		EnvironmentID: environment.ID,
		Version:       1,
		Description:   "Initial snapshot",
	}
	if actorID != nil {
		actor := strings.TrimSpace(*actorID)
		if actor != "" {
			version.CreatedBy = &actor
		}
	}
	if err := s.envs.CreateEnvironmentVersion(ctx, version, nil); err != nil {
		return err
	}
	return s.recordAudit(ctx, environment.ProjectID, environment.ID, version.ID, actorID, "version_seeded", map[string]any{
		"version": version.Version,
	})
}

func (s Service) decryptVariables(vars []domain.EnvironmentVariable) ([]Variable, error) {
	decrypted := make([]Variable, 0, len(vars))
	for _, item := range vars {
		key := strings.TrimSpace(item.Key)
		if key == "" {
			return nil, errVariableKeyRequired
		}
		plain, err := crypto.DecryptToString(s.cfg.EnvEncryptionKey, item.Value)
		if err != nil {
			return nil, err
		}
		checksum := ""
		if item.Checksum != nil && *item.Checksum != "" {
			checksum = *item.Checksum
		} else {
			checksum = checksumOf(plain)
		}
		decrypted = append(decrypted, Variable{Key: key, Value: plain, Checksum: checksum})
	}
	return decrypted, nil
}

func (s Service) prepareVariables(inputs []VariableInput) ([]domain.EnvironmentVariable, error) {
	vars := make([]domain.EnvironmentVariable, 0, len(inputs))
	for _, input := range inputs {
		key := strings.TrimSpace(input.Key)
		if key == "" {
			return nil, errVariableKeyRequired
		}
		ciphertext, err := crypto.EncryptString(s.cfg.EnvEncryptionKey, input.Value)
		if err != nil {
			return nil, err
		}
		checksum := checksumOf(input.Value)
		vars = append(vars, domain.EnvironmentVariable{
			Key:       key,
			Value:     ciphertext,
			Checksum:  &checksum,
			CreatedAt: time.Now().UTC(),
		})
	}
	return vars, nil
}

func (s Service) recordAudit(ctx context.Context, projectID, environmentID, versionID string, actorID *string, action string, metadata map[string]any) error {
	metaBytes, err := json.Marshal(metadata)
	if err != nil {
		return err
	}
	var envPtr *string
	if environmentID != "" {
		env := environmentID
		envPtr = &env
	}
	var versionPtr *string
	if versionID != "" {
		ver := versionID
		versionPtr = &ver
	}
	var actorPtr *string
	if actorID != nil {
		actor := strings.TrimSpace(*actorID)
		if actor != "" {
			actorPtr = &actor
		}
	}
	audit := &domain.EnvironmentAudit{
		ProjectID:     projectID,
		EnvironmentID: envPtr,
		VersionID:     versionPtr,
		ActorID:       actorPtr,
		Action:        action,
		Metadata:      metaBytes,
		CreatedAt:     time.Now().UTC(),
	}
	return s.envs.InsertEnvironmentAudit(ctx, audit)
}

func normalizeSlug(value string) string {
	base := strings.ToLower(strings.TrimSpace(value))
	if base == "" {
		return ""
	}
	base = strings.ReplaceAll(base, "_", "-")
	base = slugExpr.ReplaceAllString(base, "-")
	for strings.Contains(base, "--") {
		base = strings.ReplaceAll(base, "--", "-")
	}
	return strings.Trim(base, "-")
}

func checksumOf(value string) string {
	sum := sha256.Sum256([]byte(value))
	return hex.EncodeToString(sum[:])
}
