package mongodb

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"

	"go.uber.org/zap"
)

// MongoDumper handles MongoDB dump operations
type MongoDumper struct {
	config DumperConfig
	logger *zap.Logger
}

// NewMongoDumper creates a new MongoDB dumper
func NewMongoDumper(cfg DumperConfig) (*MongoDumper, error) {
	// Verify mongodump is available
	if _, err := exec.LookPath("mongodump"); err != nil {
		return nil, ErrMongoDumpNotFound
	}

	return &MongoDumper{
		config: cfg,
		logger: cfg.Logger,
	}, nil
}

// CreateDump creates a MongoDB dump using mongodump
func (d *MongoDumper) CreateDump(ctx context.Context, outputPath string) error {
	d.logger.Info("Starting MongoDB dump", zap.String("output", outputPath))

	// Check if the URI already contains a database name
	uriContainsDB := strings.Contains(d.config.MongoURI, "?") &&
		strings.Contains(d.config.MongoURI, "/") &&
		len(strings.Split(strings.Split(d.config.MongoURI, "?")[0], "/")) > 3

	// Create the output directory if it doesn't exist
	if err := os.MkdirAll(outputPath, 0755); err != nil {
		return fmt.Errorf("failed to create output directory: %w", err)
	}

	// Build mongodump arguments - use --out instead of --archive
	args := []string{"--uri", d.config.MongoURI, "--out", outputPath}

	// Only add the --db parameter if a database is specified AND the URI doesn't already contain one
	if d.config.Database != "" && !uriContainsDB {
		args = append(args, "--db", d.config.Database)
	}

	// Add progress reporting parameters
	args = append(args, "--verbose")

	// Log the command being executed (with the URI redacted)
	cmdString := fmt.Sprintf("mongodump --uri [REDACTED] --out=%s --verbose", outputPath)
	if d.config.Database != "" && !uriContainsDB {
		cmdString += fmt.Sprintf(" --db %s", d.config.Database)
	}
	d.logger.Debug("Executing command", zap.String("command", cmdString))

	cmd := exec.CommandContext(ctx, "mongodump", args...)

	// Capture command output for logging
	var stdoutBuf, stderrBuf strings.Builder
	stdout, stderr, err := setupCommandOutput(cmd)
	if err != nil {
		return fmt.Errorf("failed to set up command output capture: %w", err)
	}

	// Track start time
	startTime := time.Now()

	if err := cmd.Start(); err != nil {
		return fmt.Errorf("failed to start mongodump: %w", err)
	}

	// Process mongodump output with progress tracking
	progressCh := make(chan struct{})
	go func() {
		scanner := bufio.NewScanner(stdout)
		lastPercentage := 0
		progressRegex := regexp.MustCompile(`(\d+)%`)
		collectionRegex := regexp.MustCompile(`writing ([^ ]+) to`)
		var currentCollection string

		for scanner.Scan() {
			line := scanner.Text()
			stdoutBuf.WriteString(line + "\n")

			// Track which collection is being dumped
			if match := collectionRegex.FindStringSubmatch(line); len(match) > 1 {
				currentCollection = match[1]
				d.logger.Info("Dumping collection",
					zap.String("collection", currentCollection))
			}

			// Look for percentage indicators in verbose output
			if match := progressRegex.FindStringSubmatch(line); len(match) > 1 {
				if pct, err := strconv.Atoi(match[1]); err == nil {
					// Only log when percentage changes significantly (at least 10%)
					if pct >= lastPercentage+10 || pct == 100 {
						if currentCollection != "" {
							d.logger.Info("MongoDB dump progress",
								zap.String("collection", currentCollection),
								zap.Int("percent_complete", pct),
								zap.Duration("elapsed", time.Since(startTime)))
						} else {
							d.logger.Info("MongoDB dump progress",
								zap.Int("percent_complete", pct),
								zap.Duration("elapsed", time.Since(startTime)))
						}
						lastPercentage = pct
					}
				}
			}

			d.logger.Debug("mongodump stdout", zap.String("output", line))
		}
		close(progressCh)
	}()

	// Capture stderr in a separate goroutine
	go func() {
		scanner := bufio.NewScanner(stderr)
		for scanner.Scan() {
			line := scanner.Text()
			stderrBuf.WriteString(line + "\n")
			d.logger.Debug("mongodump stderr", zap.String("output", line))
		}
	}()

	// Wait for command to complete
	err = cmd.Wait()
	<-progressCh // Wait for stdout processing to complete

	duration := time.Since(startTime)

	if err != nil {
		// If there was an error, log the output at ERROR level
		d.logger.Error("MongoDB dump failed",
			zap.Error(err),
			zap.String("stdout", stdoutBuf.String()),
			zap.String("stderr", stderrBuf.String()),
			zap.Duration("duration", duration))

		return fmt.Errorf("mongodump failed: %w - stderr: %s", err, stderrBuf.String())
	}

	// Count collections and calculate total size
	var totalSize int64
	var collectionCount int

	err = filepath.Walk(outputPath, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if !info.IsDir() && filepath.Ext(path) == ".bson" {
			collectionCount++
			totalSize += info.Size()
		}
		return nil
	})

	if err != nil {
		d.logger.Warn("Failed to calculate dump statistics", zap.Error(err))
	}

	// Get directory size for reporting
	var sizeStr string

	// Less than 1MB - show in KB
	if totalSize < 1024*1024 {
		sizeKB := float64(totalSize) / 1024
		sizeStr = fmt.Sprintf("%.2f KB", sizeKB)
		d.logger.Info("MongoDB dump completed successfully",
			zap.String("output_dir", outputPath),
			zap.Duration("duration", duration),
			zap.Int64("size_bytes", totalSize),
			zap.String("file_size", sizeStr),
			zap.Int("collection_count", collectionCount),
			zap.Float64("kb_per_sec", sizeKB/duration.Seconds()))
	} else if totalSize < 1024*1024*1024 { // Between 1MB and 1GB - show in MB
		sizeMB := float64(totalSize) / 1024 / 1024
		sizeStr = fmt.Sprintf("%.2f MB", sizeMB)
		d.logger.Info("MongoDB dump completed successfully",
			zap.String("output_dir", outputPath),
			zap.Duration("duration", duration),
			zap.Int64("size_bytes", totalSize),
			zap.String("file_size", sizeStr),
			zap.Int("collection_count", collectionCount),
			zap.Float64("mb_per_sec", sizeMB/duration.Seconds()))
	} else { // Larger than 1GB - show in GB with MB in parentheses
		sizeMB := float64(totalSize) / 1024 / 1024
		sizeGB := sizeMB / 1024
		sizeStr = fmt.Sprintf("%.2f GB (%.2f MB)", sizeGB, sizeMB)
		d.logger.Info("MongoDB dump completed successfully",
			zap.String("output_dir", outputPath),
			zap.Duration("duration", duration),
			zap.Int64("size_bytes", totalSize),
			zap.String("file_size", sizeStr),
			zap.Int("collection_count", collectionCount),
			zap.Float64("mb_per_sec", sizeMB/duration.Seconds()))
	}

	return nil
}

// streamOutput reads from a reader and logs it line by line
func (d *MongoDumper) streamOutput(r io.Reader, prefix string) {
	scanner := bufio.NewScanner(r)
	for scanner.Scan() {
		line := scanner.Text()
		if strings.TrimSpace(line) != "" {
			d.logger.Debug(prefix, zap.String("output", line))
		}
	}

	if err := scanner.Err(); err != nil && err != io.EOF {
		d.logger.Error("Error reading command output", zap.Error(err))
	}
}

// GenerateBackupFilename generates backup paths and S3 keys
func (d *MongoDumper) GenerateBackupFilename() (string, string, string) {
	timestamp := time.Now().UTC().Format("2006-01-02T15-04-05Z")

	// Use environment or default to "default"
	environment := d.config.GetEnvironment("default")
	if environment == "default" {
		d.logger.Info("No environment specified, using 'default' for backup paths")
	}

	// Use database name or default to "all-databases"
	dbName := d.config.GetDatabase("all-databases")

	// Create directory name and S3 key prefix
	backupDirName := fmt.Sprintf("%s-%s-%s", dbName, environment, timestamp)
	localBackupPath := filepath.Join(d.config.TempDir, backupDirName)
	s3Key := fmt.Sprintf("%s/%s/%s", environment, time.Now().Format("2006-01-02"), backupDirName)

	return backupDirName, localBackupPath, s3Key
}

// setupCommandOutput sets up pipes for command stdout and stderr
func setupCommandOutput(cmd *exec.Cmd) (io.ReadCloser, io.ReadCloser, error) {
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
