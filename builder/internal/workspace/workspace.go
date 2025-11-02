package workspace

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// Manager owns deployment-specific working directories under a common root.
type Manager struct {
	root string
}

// New ensures the workspace root exists and is accessible.
func New(root string) (*Manager, error) {
	if root == "" {
		return nil, fmt.Errorf("workspace root cannot be empty")
	}
	if err := os.MkdirAll(root, 0o755); err != nil {
		return nil, fmt.Errorf("create workspace root: %w", err)
	}
	return &Manager{root: root}, nil
}

// Prepare creates an isolated directory for the provided identifier.
func (m *Manager) Prepare(identifier string) (string, error) {
	if identifier == "" {
		return "", fmt.Errorf("workspace identifier cannot be empty")
	}
	dir := filepath.Join(m.root, identifier)
	if err := os.RemoveAll(dir); err != nil {
		return "", fmt.Errorf("cleanup workspace: %w", err)
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", fmt.Errorf("create workspace: %w", err)
	}
	return dir, nil
}

// Cleanup removes the workspace directory.
func (m *Manager) Cleanup(path string) error {
	if path == "" {
		return nil
	}
	// Ensure we only remove directories within the configured root.
	rel, err := filepath.Rel(m.root, path)
	if err != nil || rel == "." || rel == "" || strings.HasPrefix(rel, "..") {
		return fmt.Errorf("refusing to cleanup path outside workspace root")
	}
	return os.RemoveAll(path)
}

// CleanupByID removes the workspace associated with the provided identifier.
func (m *Manager) CleanupByID(identifier string) error {
	if identifier == "" {
		return fmt.Errorf("workspace identifier cannot be empty")
	}
	return m.Cleanup(filepath.Join(m.root, identifier))
}
