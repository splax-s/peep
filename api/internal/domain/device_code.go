package domain

import "time"

// DeviceCodeStatus enumerates valid device authorization states.
const (
	DeviceCodeStatusPending  = "pending"
	DeviceCodeStatusApproved = "approved"
	DeviceCodeStatusConsumed = "consumed"
	DeviceCodeStatusExpired  = "expired"
)

// DeviceCode tracks the lifecycle of a device authorization request.
type DeviceCode struct {
	DeviceCode      string
	UserCode        string
	VerificationURL string
	Status          string
	UserID          *string
	ExpiresAt       time.Time
	IntervalSeconds int
	CreatedAt       time.Time
	ApprovedAt      *time.Time
	ConsumedAt      *time.Time
	LastPolledAt    *time.Time
}

// Expired reports whether the device code is expired relative to now.
func (d DeviceCode) Expired(now time.Time) bool {
	if d.ExpiresAt.IsZero() {
		return false
	}
	return now.UTC().After(d.ExpiresAt.UTC())
}
