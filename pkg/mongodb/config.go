package mongodb

import (
	"errors"
	"os/exec"

	"go.uber.org/zap"
)

// ErrMongoDumpNotFound is returned when the mongodump executable is not found in PATH
var ErrMongoDumpNotFound = errors.New("mongodump executable not found in PATH")

// DumperConfig contains configuration for MongoDB backup
type DumperConfig struct {
	// MongoDB connection details
	MongoURI    string
	Database    string
	Environment string // "staging" or "production"

	// S3/Backblaze configuration
	S3Endpoint  string
	S3Region    string
	S3Bucket    string
	S3AccessKey string
	S3SecretKey string

	// Local temporary storage
	TempDir string

	// Logger
	Logger *zap.Logger // Keep this as zap.Logger for backward compatibility
}

// Validate checks if the configuration is valid
func (c *DumperConfig) Validate() error {
	// Check for required fields
	if c.MongoURI == "" {
		return errors.New("MongoDB URI is required")
	}

	if c.S3Endpoint == "" || c.S3Bucket == "" || c.S3AccessKey == "" || c.S3SecretKey == "" {
		return errors.New("S3 configuration is incomplete")
	}

	// Verify mongodump is available
	if _, err := exec.LookPath("mongodump"); err != nil {
		return ErrMongoDumpNotFound
	}

	return nil
}

// GetEnvironment returns the environment or a default value if not specified
func (c *DumperConfig) GetEnvironment(defaultValue string) string {
	if c.Environment == "" {
		return defaultValue
	}
	return c.Environment
}

// GetDatabase returns the database name or a default value if not specified
func (c *DumperConfig) GetDatabase(defaultValue string) string {
	if c.Database == "" {
		return defaultValue
	}
	return c.Database
}
