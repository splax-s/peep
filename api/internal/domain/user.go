package domain

import "time"

// User represents a platform account.
type User struct {
	ID           string
	Email        string
	PasswordHash []byte
	CreatedAt    time.Time
}
