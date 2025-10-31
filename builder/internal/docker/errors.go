package docker

import "errors"

// ErrNotFound indicates the requested Docker resource was not found.
var ErrNotFound = errors.New("docker: resource not found")
