package postgres

import (
	"context"
	"database/sql"
	"errors"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"

	"github.com/splax/localvercel/api/internal/domain"
	"github.com/splax/localvercel/api/internal/repository"
)

const (
	deviceCodeInsert = `INSERT INTO device_codes (
		device_code,
		user_code,
		verification_url,
		status,
		expires_at,
		interval_seconds,
		created_at
	) VALUES (
		$1,$2,$3,$4,$5,$6,NOW()
	)`
	deviceCodeSelect       = `SELECT device_code, user_code, verification_url, status, user_id, expires_at, interval_seconds, created_at, approved_at, consumed_at, last_polled_at FROM device_codes WHERE device_code = $1`
	deviceCodeSelectByCode = `SELECT device_code, user_code, verification_url, status, user_id, expires_at, interval_seconds, created_at, approved_at, consumed_at, last_polled_at FROM device_codes WHERE user_code = $1`
)

// CreateDeviceCode persists a new device authorization request.
func (r *Repository) CreateDeviceCode(ctx context.Context, code *domain.DeviceCode) error {
	if code == nil {
		return repository.ErrInvalidArgument
	}
	userCode := strings.ToUpper(strings.TrimSpace(code.UserCode))
	if userCode == "" {
		return repository.ErrInvalidArgument
	}
	status := strings.TrimSpace(code.Status)
	if status == "" {
		status = domain.DeviceCodeStatusPending
	}
	_, err := r.pool.Exec(ctx, deviceCodeInsert,
		strings.TrimSpace(code.DeviceCode),
		userCode,
		strings.TrimSpace(code.VerificationURL),
		status,
		code.ExpiresAt.UTC(),
		code.IntervalSeconds,
	)
	if err != nil {
		var pgErr *pgconn.PgError
		if errors.As(err, &pgErr) {
			switch pgErr.Code {
			case "23505":
				return repository.ErrInvalidArgument
			}
		}
		return err
	}
	code.UserCode = userCode
	code.Status = status
	code.CreatedAt = time.Now().UTC()
	return nil
}

// GetDeviceCode fetches a device code by its device identifier.
func (r *Repository) GetDeviceCode(ctx context.Context, deviceCode string) (*domain.DeviceCode, error) {
	row := r.pool.QueryRow(ctx, deviceCodeSelect, strings.TrimSpace(deviceCode))
	return scanDeviceCode(row)
}

// GetDeviceCodeByUserCode fetches a device authorization via user code.
func (r *Repository) GetDeviceCodeByUserCode(ctx context.Context, userCode string) (*domain.DeviceCode, error) {
	row := r.pool.QueryRow(ctx, deviceCodeSelectByCode, strings.ToUpper(strings.TrimSpace(userCode)))
	return scanDeviceCode(row)
}

// MarkDeviceCodeApproved associates the device code with a user and marks it approved.
func (r *Repository) MarkDeviceCodeApproved(ctx context.Context, deviceCode, userID string) (*domain.DeviceCode, error) {
	const query = `UPDATE device_codes
		SET status = 'approved',
			user_id = $2,
			approved_at = NOW()
		WHERE device_code = $1
			AND status = 'pending'
			AND expires_at > NOW()
		RETURNING device_code, user_code, verification_url, status, user_id, expires_at, interval_seconds, created_at, approved_at, consumed_at, last_polled_at`
	row := r.pool.QueryRow(ctx, query, strings.TrimSpace(deviceCode), strings.TrimSpace(userID))
	code, err := scanDeviceCode(row)
	if err != nil {
		if errors.Is(err, repository.ErrNotFound) {
			return nil, repository.ErrInvalidArgument
		}
		return nil, err
	}
	return code, nil
}

// ConsumeDeviceCode transitions the device code to consumed and returns the associated user id.
func (r *Repository) ConsumeDeviceCode(ctx context.Context, deviceCode string) (string, error) {
	const query = `UPDATE device_codes
		SET status = 'consumed',
			consumed_at = NOW()
		WHERE device_code = $1
			AND status = 'approved'
		RETURNING user_id`
	row := r.pool.QueryRow(ctx, query, strings.TrimSpace(deviceCode))
	var userID sql.NullString
	if err := row.Scan(&userID); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return "", repository.ErrInvalidArgument
		}
		return "", err
	}
	if !userID.Valid {
		return "", repository.ErrInvalidArgument
	}
	return strings.TrimSpace(userID.String), nil
}

// MarkDeviceCodeExpired marks the code as expired if it is still pending.
func (r *Repository) MarkDeviceCodeExpired(ctx context.Context, deviceCode string) error {
	const query = `UPDATE device_codes
		SET status = 'expired'
		WHERE device_code = $1 AND status IN ('pending', 'approved')`
	_, err := r.pool.Exec(ctx, query, strings.TrimSpace(deviceCode))
	return err
}

// TouchDeviceCode updates the last poll timestamp for observability.
func (r *Repository) TouchDeviceCode(ctx context.Context, deviceCode string, ts time.Time) error {
	const query = `UPDATE device_codes SET last_polled_at = $2 WHERE device_code = $1`
	_, err := r.pool.Exec(ctx, query, strings.TrimSpace(deviceCode), ts.UTC())
	return err
}

func scanDeviceCode(row pgx.Row) (*domain.DeviceCode, error) {
	var (
		code       domain.DeviceCode
		userID     sql.NullString
		approvedAt sql.NullTime
		consumedAt sql.NullTime
		polledAt   sql.NullTime
	)
	if err := row.Scan(
		&code.DeviceCode,
		&code.UserCode,
		&code.VerificationURL,
		&code.Status,
		&userID,
		&code.ExpiresAt,
		&code.IntervalSeconds,
		&code.CreatedAt,
		&approvedAt,
		&consumedAt,
		&polledAt,
	); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, repository.ErrNotFound
		}
		return nil, err
	}
	if userID.Valid {
		val := strings.TrimSpace(userID.String)
		code.UserID = &val
	}
	if approvedAt.Valid {
		value := approvedAt.Time.UTC()
		code.ApprovedAt = &value
	}
	if consumedAt.Valid {
		value := consumedAt.Time.UTC()
		code.ConsumedAt = &value
	}
	if polledAt.Valid {
		value := polledAt.Time.UTC()
		code.LastPolledAt = &value
	}
	code.UserCode = strings.TrimSpace(code.UserCode)
	code.Status = strings.TrimSpace(code.Status)
	return &code, nil
}
