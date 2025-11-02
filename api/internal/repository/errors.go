package repository

import "errors"

// ErrNotFound indicates an entity was not located.
var ErrNotFound = errors.New("repository: not found")

// ErrInvalidArgument indicates the provided input could not be persisted.
var ErrInvalidArgument = errors.New("repository: invalid argument")
