package auth

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/splax/localvercel/api/internal/domain"
	"github.com/splax/localvercel/api/internal/repository"
	"github.com/splax/localvercel/pkg/crypto"
)

var (
	ErrDeviceAuthDisabled    = errors.New("auth: device authorization disabled")
	ErrDeviceCodeInvalid     = errors.New("auth: device code invalid")
	ErrDeviceCodeExpired     = errors.New("auth: device code expired")
	ErrDeviceCodeConsumed    = errors.New("auth: device code consumed")
	ErrDeviceCodePending     = errors.New("auth: authorization pending")
	ErrDeviceCodeNotApproved = errors.New("auth: device code not approved")
)

// DevicePollResult captures the outcome of a device poll attempt.
type DevicePollResult struct {
	Status    string
	Tokens    *TokenPair
	ExpiresIn time.Duration
	Interval  time.Duration
}

// StartDeviceAuthorization issues a new device authorization challenge for CLI logins.
func (s Service) StartDeviceAuthorization(ctx context.Context) (*domain.DeviceCode, error) {
	if s.deviceCodes == nil {
		return nil, ErrDeviceAuthDisabled
	}
	ttl := s.cfg.DeviceCodeTTL
	if ttl <= 0 {
		ttl = 10 * time.Minute
	}
	interval := s.cfg.DeviceCodePollInterval
	if interval <= 0 {
		interval = 5 * time.Second
	}
	codeLen := s.cfg.DeviceCodeUserCodeLength
	if codeLen < 6 {
		codeLen = 8
	}
	if codeLen%2 != 0 {
		codeLen++
	}
	verificationURL := strings.TrimSpace(s.cfg.DeviceVerificationURL)
	if verificationURL == "" {
		verificationURL = "https://dashboard.peep.com/device"
	}
	now := time.Now().UTC()
	var lastErr error
	for attempt := 0; attempt < 5; attempt++ {
		deviceToken, err := randomDeviceToken()
		if err != nil {
			return nil, err
		}
		userCode, err := randomUserCode(codeLen)
		if err != nil {
			return nil, err
		}
		record := domain.DeviceCode{
			DeviceCode:      deviceToken,
			UserCode:        userCode,
			VerificationURL: verificationURL,
			Status:          domain.DeviceCodeStatusPending,
			ExpiresAt:       now.Add(ttl),
			IntervalSeconds: int(interval / time.Second),
			CreatedAt:       now,
		}
		if record.IntervalSeconds <= 0 {
			record.IntervalSeconds = 5
		}
		if err := s.deviceCodes.CreateDeviceCode(ctx, &record); err != nil {
			if errors.Is(err, repository.ErrInvalidArgument) {
				lastErr = err
				continue
			}
			return nil, err
		}
		return &record, nil
	}
	if lastErr != nil {
		return nil, lastErr
	}
	return nil, repository.ErrInvalidArgument
}

// VerifyDeviceCode authenticates the user and approves the pending device code.
func (s Service) VerifyDeviceCode(ctx context.Context, userCode, email, password string) (*domain.DeviceCode, error) {
	if s.deviceCodes == nil {
		return nil, ErrDeviceAuthDisabled
	}
	trimmedCode := strings.ToUpper(strings.TrimSpace(userCode))
	if trimmedCode == "" {
		return nil, ErrDeviceCodeInvalid
	}
	code, err := s.deviceCodes.GetDeviceCodeByUserCode(ctx, trimmedCode)
	if err != nil {
		if errors.Is(err, repository.ErrNotFound) {
			return nil, ErrDeviceCodeInvalid
		}
		return nil, err
	}
	now := time.Now().UTC()
	if code.Expired(now) || code.Status == domain.DeviceCodeStatusExpired {
		_ = s.deviceCodes.MarkDeviceCodeExpired(ctx, code.DeviceCode)
		return nil, ErrDeviceCodeExpired
	}
	if code.Status == domain.DeviceCodeStatusConsumed {
		return nil, ErrDeviceCodeConsumed
	}
	if code.Status == domain.DeviceCodeStatusApproved {
		return code, nil
	}
	user, err := s.users.GetUserByEmail(ctx, email)
	if err != nil {
		return nil, err
	}
	if err := crypto.ComparePassword(user.PasswordHash, password); err != nil {
		return nil, err
	}
	updated, err := s.deviceCodes.MarkDeviceCodeApproved(ctx, code.DeviceCode, user.ID)
	if err != nil {
		if errors.Is(err, repository.ErrInvalidArgument) {
			return nil, ErrDeviceCodeInvalid
		}
		return nil, err
	}
	return updated, nil
}

// PollDeviceCode checks the status of a device authorization and issues tokens when approved.
func (s Service) PollDeviceCode(ctx context.Context, deviceCode string) (DevicePollResult, error) {
	if s.deviceCodes == nil {
		return DevicePollResult{}, ErrDeviceAuthDisabled
	}
	trimmed := strings.TrimSpace(deviceCode)
	if trimmed == "" {
		return DevicePollResult{}, ErrDeviceCodeInvalid
	}
	code, err := s.deviceCodes.GetDeviceCode(ctx, trimmed)
	if err != nil {
		if errors.Is(err, repository.ErrNotFound) {
			return DevicePollResult{}, ErrDeviceCodeInvalid
		}
		return DevicePollResult{}, err
	}
	now := time.Now().UTC()
	defer func() {
		_ = s.deviceCodes.TouchDeviceCode(ctx, trimmed, now)
	}()
	if code.Expired(now) || code.Status == domain.DeviceCodeStatusExpired {
		_ = s.deviceCodes.MarkDeviceCodeExpired(ctx, trimmed)
		return DevicePollResult{Status: domain.DeviceCodeStatusExpired}, ErrDeviceCodeExpired
	}
	remaining := code.ExpiresAt.Sub(now)
	if remaining < 0 {
		remaining = 0
	}
	interval := time.Duration(code.IntervalSeconds) * time.Second
	if interval <= 0 {
		interval = time.Second * 5
	}
	switch code.Status {
	case domain.DeviceCodeStatusPending:
		return DevicePollResult{Status: domain.DeviceCodeStatusPending, ExpiresIn: remaining, Interval: interval}, ErrDeviceCodePending
	case domain.DeviceCodeStatusApproved:
		if code.UserID == nil || strings.TrimSpace(*code.UserID) == "" {
			return DevicePollResult{}, ErrDeviceCodeNotApproved
		}
		userID, err := s.deviceCodes.ConsumeDeviceCode(ctx, trimmed)
		if err != nil {
			if errors.Is(err, repository.ErrInvalidArgument) {
				return DevicePollResult{}, ErrDeviceCodeInvalid
			}
			return DevicePollResult{}, err
		}
		tokens, err := s.issueTokens(userID, "")
		if err != nil {
			return DevicePollResult{}, err
		}
		return DevicePollResult{Status: domain.DeviceCodeStatusApproved, Tokens: &tokens, ExpiresIn: remaining, Interval: interval}, nil
	case domain.DeviceCodeStatusConsumed:
		return DevicePollResult{Status: domain.DeviceCodeStatusConsumed, ExpiresIn: remaining, Interval: interval}, ErrDeviceCodeConsumed
	default:
		return DevicePollResult{Status: code.Status, ExpiresIn: remaining, Interval: interval}, ErrDeviceCodeInvalid
	}
}

func randomDeviceToken() (string, error) {
	buf := make([]byte, 32)
	if _, err := rand.Read(buf); err != nil {
		return "", fmt.Errorf("generate device token: %w", err)
	}
	return strings.TrimRight(base64.RawURLEncoding.EncodeToString(buf), "="), nil
}

func randomUserCode(length int) (string, error) {
	if length <= 0 {
		length = 8
	}
	const alphabet = "ABCDEFGHJKLMNPQRSTUVWXYZ23456789"
	buf := make([]byte, length)
	if _, err := rand.Read(buf); err != nil {
		return "", fmt.Errorf("generate user code: %w", err)
	}
	for i := 0; i < length; i++ {
		buf[i] = alphabet[int(buf[i])%len(alphabet)]
	}
	code := string(buf)
	if length >= 8 {
		return fmt.Sprintf("%s-%s", code[:length/2], code[length/2:]), nil
	}
	return code, nil
}
