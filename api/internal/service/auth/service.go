package auth

import (
	"context"
	"errors"
	"strings"
	"time"

	"log/slog"

	"github.com/google/uuid"

	"github.com/splax/localvercel/api/internal/domain"
	"github.com/splax/localvercel/api/internal/repository"
	"github.com/splax/localvercel/pkg/config"
	"github.com/splax/localvercel/pkg/crypto"
	jwtpkg "github.com/splax/localvercel/pkg/jwt"
)

// Service handles authentication workflows.
type Service struct {
	users       repository.UserRepository
	deviceCodes repository.DeviceCodeRepository
	logger      *slog.Logger
	cfg         config.APIConfig
}

// New constructs a Service.
func New(users repository.UserRepository, devices repository.DeviceCodeRepository, logger *slog.Logger, cfg config.APIConfig) Service {
	return Service{users: users, deviceCodes: devices, logger: logger, cfg: cfg}
}

// TokenPair contains access and refresh tokens.
type TokenPair struct {
	AccessToken  string
	RefreshToken string
	ExpiresIn    time.Duration
}

// Signup registers a new user.
func (s Service) Signup(ctx context.Context, email, password string) (*domain.User, TokenPair, error) {
	hash, err := crypto.HashPassword(password)
	if err != nil {
		return nil, TokenPair{}, err
	}
	user := &domain.User{
		ID:           uuid.NewString(),
		Email:        email,
		PasswordHash: hash,
		CreatedAt:    time.Now().UTC(),
	}
	if err := s.users.CreateUser(ctx, user); err != nil {
		return nil, TokenPair{}, err
	}
	tokens, err := s.issueTokens(user.ID, "")
	if err != nil {
		return nil, TokenPair{}, err
	}
	s.logger.Info("user registered", "user_id", user.ID)
	return user, tokens, nil
}

// Login authenticates a user and returns tokens.
func (s Service) Login(ctx context.Context, email, password string) (*domain.User, TokenPair, error) {
	user, err := s.users.GetUserByEmail(ctx, email)
	if err != nil {
		if errors.Is(err, repository.ErrNotFound) {
			return nil, TokenPair{}, err
		}
		return nil, TokenPair{}, err
	}
	if err := crypto.ComparePassword(user.PasswordHash, password); err != nil {
		return nil, TokenPair{}, err
	}
	tokens, err := s.issueTokens(user.ID, "")
	if err != nil {
		return nil, TokenPair{}, err
	}
	s.logger.Info("user logged in", "user_id", user.ID)
	return user, tokens, nil
}

// Authorize validates a bearer token and returns the associated user and claims.
func (s Service) Authorize(ctx context.Context, token string) (*domain.User, *jwtpkg.Claims, error) {
	trimmed := strings.TrimSpace(token)
	if trimmed == "" {
		return nil, nil, errors.New("token required")
	}
	claims, err := jwtpkg.Parse(trimmed, s.cfg.JWTSecret)
	if err != nil {
		return nil, nil, err
	}
	user, err := s.users.GetUserByID(ctx, claims.UserID)
	if err != nil {
		return nil, nil, err
	}
	return user, claims, nil
}

func (s Service) issueTokens(userID, teamID string) (TokenPair, error) {
	access, err := jwtpkg.GenerateToken(userID, teamID, s.cfg.JWTSecret, s.cfg.AccessTokenTTL)
	if err != nil {
		return TokenPair{}, err
	}
	refresh, err := jwtpkg.GenerateToken(userID, teamID, s.cfg.JWTSecret, s.cfg.RefreshTokenTTL)
	if err != nil {
		return TokenPair{}, err
	}
	return TokenPair{AccessToken: access, RefreshToken: refresh, ExpiresIn: s.cfg.AccessTokenTTL}, nil
}
