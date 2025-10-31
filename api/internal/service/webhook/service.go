package webhook

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"strings"

	"log/slog"

	"github.com/splax/localvercel/api/internal/repository"
	"github.com/splax/localvercel/pkg/config"
	"github.com/splax/localvercel/pkg/crypto"
)

// Service handles webhook storage and validation.
type Service struct {
	repo   repository.WebhookRepository
	logger *slog.Logger
	cfg    config.APIConfig
}

// New constructs a webhook service.
func New(repo repository.WebhookRepository, logger *slog.Logger, cfg config.APIConfig) Service {
	return Service{repo: repo, logger: logger, cfg: cfg}
}

// UpsertSecret stores encrypted secret bytes.
func (s Service) UpsertSecret(ctx context.Context, projectID string, secret string) error {
	value := strings.TrimSpace(secret)
	if value == "" {
		return errors.New("secret is required")
	}
	payload, err := crypto.EncryptString(s.cfg.EnvEncryptionKey, value)
	if err != nil {
		return err
	}
	return s.repo.UpsertWebhook(ctx, projectID, payload)
}

// ValidateSignature checks HMAC signature for payload.
func (s Service) ValidateSignature(payload []byte, secret []byte, provided string) error {
	if provided == "" {
		return errors.New("missing webhook signature")
	}
	hasher := hmac.New(sha256.New, secret)
	hasher.Write(payload)
	expected := hex.EncodeToString(hasher.Sum(nil))
	if !hmac.Equal([]byte(provided), []byte(expected)) {
		return errors.New("invalid webhook signature")
	}
	return nil
}

// CheckSignature loads the secret for a project and verifies payload signature.
func (s Service) CheckSignature(ctx context.Context, projectID string, payload []byte, provided string) error {
	secret, err := s.repo.GetWebhookSecret(ctx, projectID)
	if err != nil {
		return err
	}
	raw, err := crypto.DecryptToString(s.cfg.EnvEncryptionKey, secret)
	if err != nil {
		return err
	}
	return s.ValidateSignature(payload, []byte(raw), provided)
}
