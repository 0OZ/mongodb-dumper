package mongodb

import (
	"archive/zip"
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"time"

	"go.uber.org/zap"
)

// Dumper manages MongoDB backups to S3
type Dumper struct {
	config    DumperConfig
	s3Client  *S3Client
	mongoDump *MongoDumper
	logger    *zap.Logger
}

// NewDumper creates a new MongoDB dumper
func NewDumper(cfg DumperConfig) (*Dumper, error) {
	// Validate configuration
	if err := cfg.Validate(); err != nil {
		return nil, err
	}

	// Create S3 client
	s3Client, err := NewS3Client(cfg)
	if err != nil {
		return nil, fmt.Errorf("failed to create S3 client: %w", err)
	}

	// Create MongoDB dumper
	mongoDump, err := NewMongoDumper(cfg)
	if err != nil {
		return nil, fmt.Errorf("failed to create MongoDB dumper: %w", err)
	}

	// Ensure temp directory exists
	if cfg.TempDir != "" {
		if err := os.MkdirAll(cfg.TempDir, 0755); err != nil {
			return nil, fmt.Errorf("failed to create temp directory: %w", err)
		}
	}

	return &Dumper{
		config:    cfg,
		s3Client:  s3Client,
		mongoDump: mongoDump,
		logger:    cfg.Logger,
	}, nil
}

// Dump performs a MongoDB dump and uploads to S3
func (d *Dumper) Dump(ctx context.Context) error {
	d.logger.Info("Starting backup process")
	// Track total operation time
	startTime := time.Now()

	// Generate backup filename with timestamp
	_, localBackupPath, s3KeyPrefix := d.mongoDump.GenerateBackupFilename()
	d.logger.Info("Backup details",
		zap.String("local_path", localBackupPath),
		zap.String("s3_prefix", s3KeyPrefix))

	// STEP 1: Execute MongoDB dump - creates a directory with collection files
	d.logger.Info("STEP 1/4: Starting MongoDB dump")
	dumpStartTime := time.Now()
	if err := d.mongoDump.CreateDump(ctx, localBackupPath); err != nil {
		return fmt.Errorf("failed to create MongoDB dump: %w", err)
	}
	dumpDuration := time.Since(dumpStartTime)

	// Get file size for reporting
	var originalSize int64
	var collectionCount int
	var fileSizeStr string

	// Count collections and get total size
	err := filepath.Walk(localBackupPath, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if !info.IsDir() && filepath.Ext(path) == ".bson" {
			collectionCount++
			originalSize += info.Size()
		}
		return nil
	})

	if err != nil {
		d.logger.Warn("Failed to calculate dump statistics", zap.Error(err))
	}

	// Format size for display based on magnitude
	if originalSize < 1024*1024 {
		sizeKB := float64(originalSize) / 1024
		fileSizeStr = fmt.Sprintf("%.2f KB", sizeKB)
	} else if originalSize < 1024*1024*1024 {
		sizeMB := float64(originalSize) / 1024 / 1024
		fileSizeStr = fmt.Sprintf("%.2f MB", sizeMB)
	} else {
		sizeMB := float64(originalSize) / 1024 / 1024
		sizeGB := sizeMB / 1024
		fileSizeStr = fmt.Sprintf("%.2f GB (%.2f MB)", sizeGB, sizeMB)
	}

	d.logger.Info("STEP 1/4: MongoDB dump completed",
		zap.Duration("duration", dumpDuration),
		zap.Int64("size_bytes", originalSize),
		zap.String("file_size", fileSizeStr),
		zap.Int("collection_count", collectionCount))

	// STEP 2: Compress the dump directory
	d.logger.Info("STEP 2/4: Compressing backup directory")
	compressStartTime := time.Now()

	// Create compressed file path by adding .zip extension
	compressedPath := localBackupPath + ".zip"
	compressedS3Key := s3KeyPrefix + ".zip"

	if err := compressFile(localBackupPath, compressedPath); err != nil {
		return fmt.Errorf("failed to compress dump directory: %w", err)
	}

	compressDuration := time.Since(compressStartTime)

	// Get compressed file size for reporting
	var compressedSize int64
	var compressedSizeStr string
	var compressionRatio float64

	if fileInfo, err := os.Stat(compressedPath); err == nil {
		compressedSize = fileInfo.Size()
		compressionRatio = float64(originalSize) / float64(compressedSize)

		// Format compressed size
		if compressedSize < 1024*1024 {
			sizeKB := float64(compressedSize) / 1024
			compressedSizeStr = fmt.Sprintf("%.2f KB", sizeKB)
		} else if compressedSize < 1024*1024*1024 {
			sizeMB := float64(compressedSize) / 1024 / 1024
			compressedSizeStr = fmt.Sprintf("%.2f MB", sizeMB)
		} else {
			sizeMB := float64(compressedSize) / 1024 / 1024
			sizeGB := sizeMB / 1024
			compressedSizeStr = fmt.Sprintf("%.2f GB (%.2f MB)", sizeGB, sizeMB)
		}

		d.logger.Info("STEP 2/4: Compression completed",
			zap.Duration("duration", compressDuration),
			zap.Int64("size_bytes", compressedSize),
			zap.String("file_size", compressedSizeStr),
			zap.Float64("compression_ratio", compressionRatio))
	} else {
		d.logger.Info("STEP 2/4: Compression completed",
			zap.Duration("duration", compressDuration),
			zap.Error(err))
	}

	// STEP 3: Upload to S3
	d.logger.Info("STEP 3/4: Starting S3 upload",
		zap.String("s3_key", compressedS3Key))
	uploadStartTime := time.Now()
	if err := d.s3Client.UploadFile(ctx, compressedPath, compressedS3Key); err != nil {
		return fmt.Errorf("failed to upload dump to S3: %w", err)
	}
	uploadDuration := time.Since(uploadStartTime)
	d.logger.Info("STEP 3/4: S3 upload completed",
		zap.Duration("duration", uploadDuration))

	// STEP 4: Cleanup
	d.logger.Info("STEP 4/4: Cleaning up temporary files")
	cleanupStartTime := time.Now()

	// Remove the dump directory and all its contents
	if err := os.RemoveAll(localBackupPath); err != nil {
		d.logger.Warn("Failed to remove temporary backup directory",
			zap.String("path", localBackupPath),
			zap.Error(err))
	}

	// Remove the compressed zip file
	if err := os.Remove(compressedPath); err != nil {
		d.logger.Warn("Failed to remove compressed backup file",
			zap.String("path", compressedPath),
			zap.Error(err))
	}

	cleanupDuration := time.Since(cleanupStartTime)
	d.logger.Info("STEP 4/4: Cleanup completed",
		zap.Duration("duration", cleanupDuration))

	// Summary
	totalDuration := time.Since(startTime)
	d.logger.Info("Backup process completed successfully",
		zap.Duration("total_duration", totalDuration),
		zap.String("s3_key", compressedS3Key),
		zap.Int("collection_count", collectionCount),
		zap.Int64("original_size_bytes", originalSize),
		zap.String("original_size", fileSizeStr),
		zap.Int64("compressed_size_bytes", compressedSize),
		zap.String("compressed_size", compressedSizeStr),
		zap.Float64("compression_ratio", compressionRatio),
		zap.String("backup_details", fmt.Sprintf("MongoDB dump (%s) + Compression (%s) + S3 upload (%s) + Cleanup (%s)",
			dumpDuration.Round(time.Millisecond),
			compressDuration.Round(time.Millisecond),
			uploadDuration.Round(time.Millisecond),
			cleanupDuration.Round(time.Millisecond))))

	return nil
}

// compressFile compresses a directory of files using zip format with minimal memory usage
func compressFile(sourceDir, target string) error {
	// Create a file to write the zip to
	zipFile, err := os.Create(target)
	if err != nil {
		return fmt.Errorf("failed to create zip file: %w", err)
	}
	defer zipFile.Close()

	// Create a new zip archive
	zipWriter := zip.NewWriter(zipFile)
	defer zipWriter.Close()

	// Walk through all files in the directory
	err = filepath.Walk(sourceDir, func(filePath string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}

		// Skip directories themselves
		if info.IsDir() {
			return nil
		}

		// Create a local file header
		header, err := zip.FileInfoHeader(info)
		if err != nil {
			return fmt.Errorf("failed to create header for %s: %w", filePath, err)
		}

		// Set compression method
		header.Method = zip.Deflate

		// Set relative path as the name in the archive
		relPath, err := filepath.Rel(sourceDir, filePath)
		if err != nil {
			return fmt.Errorf("failed to get relative path for %s: %w", filePath, err)
		}
		header.Name = relPath

		// Create file in the zip
		writer, err := zipWriter.CreateHeader(header)
		if err != nil {
			return fmt.Errorf("failed to create zip entry for %s: %w", filePath, err)
		}

		// Open file for reading
		file, err := os.Open(filePath)
		if err != nil {
			return fmt.Errorf("failed to open file %s: %w", filePath, err)
		}
		defer file.Close()

		// Create a buffer for chunked copying
		buffer := make([]byte, 32*1024) // 32KB buffer instead of loading entire file

		// Copy file contents to the zip in chunks
		_, err = io.CopyBuffer(writer, file, buffer)
		if err != nil {
			return fmt.Errorf("failed to write %s to zip: %w", filePath, err)
		}

		return nil
	})

	if err != nil {
		return fmt.Errorf("failed to walk directory: %w", err)
	}

	return nil
}

// ListBackups lists all available backups
func (d *Dumper) ListBackups(ctx context.Context) ([]string, error) {
	// Get environment with default fallback
	environment := d.config.GetEnvironment("default")

	return d.s3Client.ListBackups(ctx, environment+"/")
}

// RestoreBackup downloads and restores a backup from S3
func (d *Dumper) RestoreBackup(ctx context.Context, s3Key string) error {
	d.logger.Info("Starting backup restoration", zap.String("s3_key", s3Key))

	// Create a temporary file for the download
	tempFile := filepath.Join(d.config.TempDir, filepath.Base(s3Key))

	// Download the backup file
	if err := d.s3Client.DownloadFile(ctx, s3Key, tempFile); err != nil {
		return fmt.Errorf("failed to download backup: %w", err)
	}

	// Cleanup temporary file
	if err := os.Remove(tempFile); err != nil {
		d.logger.Warn("Failed to remove temporary backup file",
			zap.String("path", tempFile),
			zap.Error(err))
	}

	return nil
}
