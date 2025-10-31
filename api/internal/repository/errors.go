package repository

import "errors"

// ErrNotFound indicates an entity was not located.
var ErrNotFound = errors.New("repository: not found")
