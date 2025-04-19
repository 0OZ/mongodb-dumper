package main

import (
	"context"
	"dumper/pkg/logger"
	"dumper/pkg/mongodb"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"time"
)

func main() {
	var envFile string
	var appLogger *logger.Logger
	// Determine log format

	// Create a temporary FlagSet just to parse the env-file flag
	tempFlags := flag.NewFlagSet("temp", flag.ContinueOnError)
	tempEnvFile := tempFlags.String("env-file", ".env", "")
	// Silence errors as we're only interested in the env-file flag
	tempFlags.SetOutput(io.Discard)
	_ = tempFlags.Parse(os.Args[1:])
	envFile = *tempEnvFile

	// Get a logger for early initialization
	earlyLogger := logger.New()

	// Load .env file first
	if envFile != "" {
		earlyLogger.Info("Loading environment variables from file", "file", envFile)
		if err := loadEnv(envFile); err != nil {
			earlyLogger.Warn("Failed to load environment file", "file", envFile, "error", err)
		} else {
			earlyLogger.Info("Successfully loaded environment variables from file")
		}
	}

	// Reset flags to ensure we start from scratch
	flag.CommandLine = flag.NewFlagSet(os.Args[0], flag.ExitOnError)

	// Now parse all command line flags - these will override any env vars
	var (
		mongoURI    = flag.String("mongo-uri", os.Getenv("MONGO_URI"), "MongoDB connection string URI")
		database    = flag.String("database", os.Getenv("MONGO_DATABASE"), "MongoDB database name (optional)")
		environment = flag.String("env", os.Getenv("ENVIRONMENT"), "Environment (staging or production)")
		s3Endpoint  = flag.String("s3-endpoint", os.Getenv("S3_ENDPOINT"), "S3 endpoint URL (Backblaze)")
		s3Region    = flag.String("s3-region", os.Getenv("S3_REGION"), "S3 region")
		s3Bucket    = flag.String("s3-bucket", os.Getenv("S3_BUCKET"), "S3 bucket name")
		s3AccessKey = flag.String("s3-access-key", os.Getenv("S3_ACCESS_KEY"), "S3 access key")
		s3SecretKey = flag.String("s3-secret-key", os.Getenv("S3_SECRET_KEY"), "S3 secret key")
		tempDir     = flag.String("temp-dir", os.Getenv("TEMP_DIR"), "Temporary directory for backups")
		interval    = flag.Duration("interval", 0, "Backup interval (default: one-time run)")
		oneTime     = flag.Bool("one-time", false, "Run a single backup and exit")
		logFormat   = flag.String("log-format", os.Getenv("LOG_FORMAT"), "Log format: json, console, pretty, compact (default: pretty)")
		// Re-add env-file flag for help text
		_ = flag.String("env-file", ".env", "Path to .env file to load environment variables from")
	)
	flag.Parse()
	var logOutputFormat logger.OutputFormat
	switch strings.ToLower(*logFormat) {
	case "json":
		logOutputFormat = logger.FormatJSON
	case "console":
		logOutputFormat = logger.FormatConsole
	case "compact":
		logOutputFormat = logger.FormatCompact
	case "pretty", "":
		logOutputFormat = logger.FormatPretty
	default:
		logOutputFormat = logger.FormatPretty
	}

	// Create logger with good defaults and application info
	logConfig := logger.Config{
		Level:         logger.InfoLevel,
		Format:        logOutputFormat,
		TimeFormat:    logger.TimeFormatISO8601,
		Output:        "stdout",
		Development:   true,
		AddCallerInfo: true,
		StackTrace:    true,
		ServiceName:   "mongodb-dumper",
		Environment:   *environment,
	}

	appLogger = logger.NewWithConfig(logConfig)

	// Log all parameters (sensitive info redacted)
	appLogger.Info("Starting MongoDB Dumper",
		"mongo_uri", redactURI(*mongoURI),
		"database", *database,
		"environment", *environment,
		"s3_endpoint", *s3Endpoint,
		"s3_region", *s3Region,
		"s3_bucket", *s3Bucket,
		"s3_access_key", redactKey(*s3AccessKey),
		"temp_dir", *tempDir,
		"interval", *interval,
		"one_time", *oneTime)

	// Validate required parameters
	if *mongoURI == "" {
		appLogger.Fatal("MongoDB URI is required", nil)
	}
	if *s3Endpoint == "" || *s3Bucket == "" || *s3AccessKey == "" || *s3SecretKey == "" {
		appLogger.Fatal("S3 configuration is incomplete", nil)
	}
	// Make environment optional by removing the required check
	// Only validate if a value is provided
	if *environment != "" && *environment != "staging" && *environment != "production" {
		appLogger.Warn("Environment should be 'staging' or 'production', using provided value anyway",
			"environment", *environment)
	}

	// Set default temp directory if not provided
	if *tempDir == "" {
		*tempDir = filepath.Join(os.TempDir(), "mongodb-dumps")
		appLogger.Info("No temporary directory specified, using default", "tempDir", *tempDir)
	}

	// Ensure temp directory exists
	if err := os.MkdirAll(*tempDir, 0755); err != nil {
		appLogger.Warn("Failed to create temporary directory", "tempDir", *tempDir, "error", err)
	}

	// Determine if this is a one-time run (either explicitly set or no interval specified)
	isOneTime := *oneTime || *interval == 0
	if isOneTime && *interval == 0 {
		appLogger.Info("No interval specified, defaulting to one-time backup")
	}

	// Create dumper configuration
	dumperConfig := mongodb.DumperConfig{
		MongoURI:    *mongoURI,
		Database:    *database,
		Environment: *environment,
		S3Endpoint:  *s3Endpoint,
		S3Region:    *s3Region,
		S3Bucket:    *s3Bucket,
		S3AccessKey: *s3AccessKey,
		S3SecretKey: *s3SecretKey,
		TempDir:     *tempDir,
		Logger:      appLogger.GetZapLogger(), // Get the underlying zap logger
	}

	// Create MongoDB dumper
	dumper, err := mongodb.NewDumper(dumperConfig)
	if err != nil {
		if errors.Is(err, mongodb.ErrMongoDumpNotFound) {
			appLogger.Fatal("MongoDB tools not found", err)
			appLogger.Info("Help: Please install MongoDB Database Tools: brew install mongodb/brew/mongodb-database-tools")
		} else {
			appLogger.Fatal("Failed to create MongoDB dumper", err)
		}
	}

	// Set up context with cancellation
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// Handle OS signals for graceful shutdown
	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, syscall.SIGINT, syscall.SIGTERM)
	go func() {
		sig := <-sigChan
		appLogger.Info("Received signal, shutting down", "signal", sig.String())
		cancel()
	}()

	// If one-time run is requested
	if isOneTime {
		appLogger.Info("Running one-time backup")
		if err := dumper.Dump(ctx); err != nil {
			appLogger.Fatal("Backup failed", err)
		}
		appLogger.Info("One-time backup completed successfully")
		return
	}

	// Run periodic backups
	appLogger.Info("Starting periodic MongoDB backups",
		"environment", *environment,
		"interval", *interval)

	ticker := time.NewTicker(*interval)
	defer ticker.Stop()

	// Perform initial backup immediately
	appLogger.Info("Running initial backup")
	if err := dumper.Dump(ctx); err != nil {
		appLogger.Error("Initial backup failed", "error", err)
	}

	// Main backup loop
	for {
		select {
		case <-ticker.C:
			appLogger.Info("Starting scheduled backup")
			if err := dumper.Dump(ctx); err != nil {
				appLogger.Error("Scheduled backup failed", "error", err)
			}
		case <-ctx.Done():
			appLogger.Info("Backup service shutting down")
			return
		}
	}
}

// loadEnv loads environment variables from a .env file
func loadEnv(filename string) error {
	data, err := os.ReadFile(filename)
	if err != nil {
		return err
	}

	lines := string(data)
	for _, line := range strings.Split(lines, "\n") {
		// Skip empty lines and comments
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}

		// Split by first equals sign
		parts := strings.SplitN(line, "=", 2)
		if len(parts) == 2 {
			key := strings.TrimSpace(parts[0])
			value := strings.TrimSpace(parts[1])
			os.Setenv(key, value)
		}
	}

	return nil
}

// getDefaultLogger returns a simple default logger for early initialization
func getDefaultLogger() *logger.Logger {
	return logger.New()
}

// redactURI redacts sensitive information from URIs
func redactURI(uri string) string {
	// Simple redaction - in a real system you'd want to parse the URI properly
	if uri == "" {
		return ""
	}
	return "[REDACTED_URI]"
}

// redactKey redacts sensitive keys
func redactKey(key string) string {
	if key == "" {
		return ""
	}
	if len(key) <= 4 {
		return "[REDACTED]"
	}
	return key[:4] + "..." + "[REDACTED]"
}

// checkMongoDumpInstalled verifies that the mongodump command is available
func checkMongoDumpInstalled(log *logger.Logger) error {
	path, err := exec.LookPath("mongodump")
	if err != nil {
		return fmt.Errorf("mongodump executable not found in PATH: %w", err)
	}
	log.Info("Found mongodump executable", "path", path)
	return nil
}
