package git

import (
	"context"
	"fmt"
	"os"
	"os/exec"
)

// Clone clones the repository into the provided destination directory.
func Clone(ctx context.Context, repoURL, dest string) error {
	if repoURL == "" {
		return fmt.Errorf("repository URL cannot be empty")
	}
	if dest == "" {
		return fmt.Errorf("destination cannot be empty")
	}
	cmd := exec.CommandContext(ctx, "git", "clone", "--depth", "1", repoURL, ".")
	cmd.Dir = dest
	// Prevent git from prompting for credentials interactively.
	cmd.Env = append(os.Environ(), "GIT_TERMINAL_PROMPT=0")
	output, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("git clone failed: %w: %s", err, string(output))
	}
	return nil
}
