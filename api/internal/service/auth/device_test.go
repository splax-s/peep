package auth

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"strings"
	"testing"
	"time"

	"github.com/splax/localvercel/api/internal/domain"
	"github.com/splax/localvercel/api/internal/repository"
	"github.com/splax/localvercel/pkg/config"
	"github.com/splax/localvercel/pkg/crypto"
)

func TestStartDeviceAuthorizationCreatesCode(t *testing.T) {
	repo := &deviceRepoMock{
		createFunc: func(_ context.Context, code *domain.DeviceCode) error {
			if code.DeviceCode == "" {
				t.Fatalf("expected device code to be generated")
			}
			if !strings.Contains(code.UserCode, "-") {
				t.Fatalf("expected user code to contain dash: %q", code.UserCode)
			}
			if code.IntervalSeconds != 5 {
				t.Fatalf("unexpected interval seconds: %d", code.IntervalSeconds)
			}
			if code.Status != domain.DeviceCodeStatusPending {
				t.Fatalf("unexpected status: %s", code.Status)
			}
			if time.Until(code.ExpiresAt) <= 0 {
				t.Fatalf("expected expiry in future")
			}
			return nil
		},
	}
	cfg := config.APIConfig{
		DeviceCodeTTL:          time.Minute,
		DeviceCodePollInterval: 5 * time.Second,
		DeviceVerificationURL:  "https://dashboard.peep.com/device",
	}
	svc := New(userRepoMock{}, repo, newLogger(), cfg)

	code, err := svc.StartDeviceAuthorization(context.Background())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if code == nil {
		t.Fatalf("expected device code returned")
	}
	if !strings.Contains(code.UserCode, "-") {
		t.Fatalf("expected formatted user code, got %q", code.UserCode)
	}
}

func TestStartDeviceAuthorizationDisabled(t *testing.T) {
	svc := New(userRepoMock{}, nil, newLogger(), config.APIConfig{})
	if _, err := svc.StartDeviceAuthorization(context.Background()); !errors.Is(err, ErrDeviceAuthDisabled) {
		t.Fatalf("expected ErrDeviceAuthDisabled, got %v", err)
	}
}

func TestVerifyDeviceCodeApprovesPendingCode(t *testing.T) {
	hashed, err := crypto.HashPassword("Testing123!")
	if err != nil {
		t.Fatalf("hash password: %v", err)
	}
	userID := "user-123"
	repo := &deviceRepoMock{
		getByUserCodeFunc: func(_ context.Context, userCode string) (*domain.DeviceCode, error) {
			if userCode != "DEVICE" {
				t.Fatalf("unexpected user code lookup: %s", userCode)
			}
			return &domain.DeviceCode{
				DeviceCode: "device-code",
				UserCode:   userCode,
				Status:     domain.DeviceCodeStatusPending,
				ExpiresAt:  time.Now().Add(time.Minute),
			}, nil
		},
		markApprovedFunc: func(_ context.Context, deviceCode, approvedUserID string) (*domain.DeviceCode, error) {
			if deviceCode != "device-code" {
				t.Fatalf("unexpected device code: %s", deviceCode)
			}
			if approvedUserID != userID {
				t.Fatalf("unexpected user id: %s", approvedUserID)
			}
			now := time.Now()
			return &domain.DeviceCode{
				DeviceCode: "device-code",
				UserCode:   "DEVICE",
				Status:     domain.DeviceCodeStatusApproved,
				UserID:     &userID,
				ApprovedAt: &now,
			}, nil
		},
	}
	users := userRepoMock{
		getByEmailFunc: func(_ context.Context, email string) (*domain.User, error) {
			if email != "device-tester@peep.dev" {
				t.Fatalf("unexpected email lookup: %s", email)
			}
			return &domain.User{ID: userID, Email: email, PasswordHash: hashed}, nil
		},
	}
	svc := New(users, repo, newLogger(), config.APIConfig{})

	code, err := svc.VerifyDeviceCode(context.Background(), "device", "device-tester@peep.dev", "Testing123!")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if code.Status != domain.DeviceCodeStatusApproved {
		t.Fatalf("expected approved status, got %s", code.Status)
	}
	if code.UserID == nil || *code.UserID != userID {
		t.Fatalf("expected user id to be set")
	}
}

func TestPollDeviceCodeReturnsTokensWhenApproved(t *testing.T) {
	userID := "user-123"
	repo := &deviceRepoMock{
		getFunc: func(_ context.Context, deviceCode string) (*domain.DeviceCode, error) {
			if deviceCode != "device-code" {
				t.Fatalf("unexpected device code lookup: %s", deviceCode)
			}
			return &domain.DeviceCode{
				DeviceCode:      deviceCode,
				Status:          domain.DeviceCodeStatusApproved,
				UserID:          &userID,
				ExpiresAt:       time.Now().Add(time.Minute),
				IntervalSeconds: 7,
			}, nil
		},
		consumeFunc: func(_ context.Context, deviceCode string) (string, error) {
			if deviceCode != "device-code" {
				t.Fatalf("unexpected consume device: %s", deviceCode)
			}
			return userID, nil
		},
	}
	cfg := config.APIConfig{
		JWTSecret:              "super-secret",
		AccessTokenTTL:         time.Minute,
		RefreshTokenTTL:        time.Hour,
		DeviceCodeTTL:          time.Minute,
		DeviceCodePollInterval: 5 * time.Second,
	}
	svc := New(userRepoMock{}, repo, newLogger(), cfg)

	result, err := svc.PollDeviceCode(context.Background(), "device-code")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Status != domain.DeviceCodeStatusApproved {
		t.Fatalf("expected approved status, got %s", result.Status)
	}
	if result.Tokens == nil || result.Tokens.AccessToken == "" {
		t.Fatalf("expected tokens to be returned")
	}
	if result.Interval != 7*time.Second {
		t.Fatalf("expected interval override, got %s", result.Interval)
	}
}

func TestPollDeviceCodePending(t *testing.T) {
	repo := &deviceRepoMock{
		getFunc: func(_ context.Context, deviceCode string) (*domain.DeviceCode, error) {
			return &domain.DeviceCode{
				DeviceCode:      deviceCode,
				Status:          domain.DeviceCodeStatusPending,
				ExpiresAt:       time.Now().Add(time.Minute),
				IntervalSeconds: 5,
			}, nil
		},
	}
	svc := New(userRepoMock{}, repo, newLogger(), config.APIConfig{})

	result, err := svc.PollDeviceCode(context.Background(), "device-code")
	if !errors.Is(err, ErrDeviceCodePending) {
		t.Fatalf("expected pending error, got %v", err)
	}
	if result.Status != domain.DeviceCodeStatusPending {
		t.Fatalf("expected pending status, got %s", result.Status)
	}
	if result.Interval != 5*time.Second {
		t.Fatalf("unexpected interval: %s", result.Interval)
	}
}

func newLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

type deviceRepoMock struct {
	createFunc        func(context.Context, *domain.DeviceCode) error
	getFunc           func(context.Context, string) (*domain.DeviceCode, error)
	getByUserCodeFunc func(context.Context, string) (*domain.DeviceCode, error)
	markApprovedFunc  func(context.Context, string, string) (*domain.DeviceCode, error)
	consumeFunc       func(context.Context, string) (string, error)
	markExpiredFunc   func(context.Context, string) error
	touchFunc         func(context.Context, string, time.Time) error
}

func (m deviceRepoMock) CreateDeviceCode(ctx context.Context, code *domain.DeviceCode) error {
	if m.createFunc != nil {
		return m.createFunc(ctx, code)
	}
	return nil
}

func (m deviceRepoMock) GetDeviceCode(ctx context.Context, deviceCode string) (*domain.DeviceCode, error) {
	if m.getFunc != nil {
		return m.getFunc(ctx, deviceCode)
	}
	return nil, repository.ErrNotFound
}

func (m deviceRepoMock) GetDeviceCodeByUserCode(ctx context.Context, userCode string) (*domain.DeviceCode, error) {
	if m.getByUserCodeFunc != nil {
		return m.getByUserCodeFunc(ctx, userCode)
	}
	return nil, repository.ErrNotFound
}

func (m deviceRepoMock) MarkDeviceCodeApproved(ctx context.Context, deviceCode, userID string) (*domain.DeviceCode, error) {
	if m.markApprovedFunc != nil {
		return m.markApprovedFunc(ctx, deviceCode, userID)
	}
	return nil, repository.ErrInvalidArgument
}

func (m deviceRepoMock) ConsumeDeviceCode(ctx context.Context, deviceCode string) (string, error) {
	if m.consumeFunc != nil {
		return m.consumeFunc(ctx, deviceCode)
	}
	return "", repository.ErrInvalidArgument
}

func (m deviceRepoMock) MarkDeviceCodeExpired(ctx context.Context, deviceCode string) error {
	if m.markExpiredFunc != nil {
		return m.markExpiredFunc(ctx, deviceCode)
	}
	return nil
}

func (m deviceRepoMock) TouchDeviceCode(ctx context.Context, deviceCode string, ts time.Time) error {
	if m.touchFunc != nil {
		return m.touchFunc(ctx, deviceCode, ts)
	}
	return nil
}

type userRepoMock struct {
	createFunc     func(context.Context, *domain.User) error
	getByEmailFunc func(context.Context, string) (*domain.User, error)
	getByIDFunc    func(context.Context, string) (*domain.User, error)
}

func (m userRepoMock) CreateUser(ctx context.Context, user *domain.User) error {
	if m.createFunc != nil {
		return m.createFunc(ctx, user)
	}
	return nil
}

func (m userRepoMock) GetUserByEmail(ctx context.Context, email string) (*domain.User, error) {
	if m.getByEmailFunc != nil {
		return m.getByEmailFunc(ctx, email)
	}
	return nil, repository.ErrNotFound
}

func (m userRepoMock) GetUserByID(ctx context.Context, id string) (*domain.User, error) {
	if m.getByIDFunc != nil {
		return m.getByIDFunc(ctx, id)
	}
	return nil, repository.ErrNotFound
}
