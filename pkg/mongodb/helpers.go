package mongodb

import (
	"fmt"
	"io"
	"os/exec"
)

// Helper functions

// GetValueOrDefault returns the value or a default if empty
func GetValueOrDefault(value, defaultValue string) string {
	if value == "" {
		return defaultValue
	}
	return value
}

// CaptureCommandOutput sets up pipes for command stdout and stderr
func CaptureCommandOutput(cmd *exec.Cmd) (io.ReadCloser, io.ReadCloser, error) {
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, nil, fmt.Errorf("failed to create stdout pipe: %w", err)
	}

	stderr, err := cmd.StderrPipe()
	if err != nil {
		return nil, nil, fmt.Errorf("failed to create stderr pipe: %w", err)
	}

	return stdout, stderr, nil
}
