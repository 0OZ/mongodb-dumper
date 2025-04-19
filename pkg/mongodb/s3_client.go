package mongodb

import (
	"context"
	"fmt"
	"io"
	"os"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/credentials"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"go.uber.org/zap"
)

// S3Client handles S3 operations
type S3Client struct {
	client *s3.Client
	bucket string
	logger *zap.Logger
}

// progressReader is used to track upload progress
type progressReader struct {
	reader        io.ReadSeeker
	totalSize     int64
	bytesRead     int64
	lastLoggedPct int
	logger        *zap.Logger
	s3Key         string
}

// Read implements io.Reader and tracks progress
func (r *progressReader) Read(p []byte) (int, error) {
	n, err := r.reader.Read(p)
	if n > 0 {
		r.bytesRead += int64(n)
		// Calculate percentage
		pct := int((float64(r.bytesRead) / float64(r.totalSize)) * 100)

		// Log progress at 10% intervals or 100%
		if pct >= r.lastLoggedPct+10 || pct == 100 {
			// Format sizes in human-readable form based on size
			var sizeStr string
			bytesUploaded := float64(r.bytesRead)
			totalSize := float64(r.totalSize)

			// Less than 1MB - show in KB
			if totalSize < 1024*1024 {
				bytesUploadedKB := bytesUploaded / 1024
				totalSizeKB := totalSize / 1024
				sizeStr = fmt.Sprintf("%.2f KB / %.2f KB", bytesUploadedKB, totalSizeKB)
			} else if totalSize < 1024*1024*1024 { // Between 1MB and 1GB - show in MB
				bytesUploadedMB := bytesUploaded / 1024 / 1024
				totalSizeMB := totalSize / 1024 / 1024
				sizeStr = fmt.Sprintf("%.2f MB / %.2f MB", bytesUploadedMB, totalSizeMB)
			} else { // Larger than 1GB - show in GB with MB in parentheses
				bytesUploadedMB := bytesUploaded / 1024 / 1024
				totalSizeMB := totalSize / 1024 / 1024
				bytesUploadedGB := bytesUploadedMB / 1024
				totalSizeGB := totalSizeMB / 1024
				sizeStr = fmt.Sprintf("%.2f GB / %.2f GB (%.2f MB / %.2f MB)",
					bytesUploadedGB, totalSizeGB, bytesUploadedMB, totalSizeMB)
			}

			r.logger.Info("Upload progress",
				zap.String("s3_key", r.s3Key),
				zap.Int("percent_complete", pct),
				zap.Int64("bytes_uploaded", r.bytesRead),
				zap.Int64("total_size", r.totalSize),
				zap.String("human_readable_size", sizeStr))
			r.lastLoggedPct = pct
		}
	}
	return n, err
}

// Seek implements io.Seeker interface
func (r *progressReader) Seek(offset int64, whence int) (int64, error) {
	// Reset read count if we seek to the beginning
	if offset == 0 && whence == io.SeekStart {
		r.bytesRead = 0
		r.lastLoggedPct = 0
	}
	return r.reader.Seek(offset, whence)
}

// NewS3Client creates a new S3 client from the configuration
func NewS3Client(cfg DumperConfig) (*S3Client, error) {
	s3Client, err := newS3ClientInternal(cfg)
	if err != nil {
		return nil, err
	}

	return &S3Client{
		client: s3Client,
		bucket: cfg.S3Bucket,
		logger: cfg.Logger,
	}, nil
}

// newS3ClientInternal configures and creates an S3 client
func newS3ClientInternal(cfg DumperConfig) (*s3.Client, error) {
	// Configure AWS SDK to use Backblaze B2's S3-compatible API
	s3Resolver := aws.EndpointResolverWithOptionsFunc(func(service, region string, options ...interface{}) (aws.Endpoint, error) {
		return aws.Endpoint{
			URL:               cfg.S3Endpoint,
			SigningRegion:     cfg.S3Region,
			HostnameImmutable: true,
			Source:            aws.EndpointSourceCustom,
		}, nil
	})

	s3Cfg, err := config.LoadDefaultConfig(context.Background(),
		config.WithEndpointResolverWithOptions(s3Resolver),
		config.WithCredentialsProvider(credentials.NewStaticCredentialsProvider(
			cfg.S3AccessKey,
			cfg.S3SecretKey,
			"",
		)),
		config.WithRegion(cfg.S3Region),
	)
	if err != nil {
		return nil, fmt.Errorf("failed to configure S3 client: %w", err)
	}

	// Create client with B2-specific options
	return s3.NewFromConfig(s3Cfg, func(o *s3.Options) {
		o.UsePathStyle = true
	}), nil
}

// UploadFile uploads a file to S3/Backblaze
func (s *S3Client) UploadFile(ctx context.Context, filePath, s3Key string) error {
	// Get file info for size
	fileInfo, err := os.Stat(filePath)
	if err != nil {
		return fmt.Errorf("failed to get file info: %w", err)
	}

	// Format file size in human-readable form
	fileSizeBytes := fileInfo.Size()
	var fileSizeStr string

	// Less than 1MB - show in KB
	if fileSizeBytes < 1024*1024 {
		fileSizeKB := float64(fileSizeBytes) / 1024
		fileSizeStr = fmt.Sprintf("%.2f KB", fileSizeKB)
	} else if fileSizeBytes < 1024*1024*1024 { // Between 1MB and 1GB - show in MB
		fileSizeMB := float64(fileSizeBytes) / 1024 / 1024
		fileSizeStr = fmt.Sprintf("%.2f MB", fileSizeMB)
	} else { // Larger than 1GB - show in GB with MB in parentheses
		fileSizeMB := float64(fileSizeBytes) / 1024 / 1024
		fileSizeGB := fileSizeMB / 1024
		fileSizeStr = fmt.Sprintf("%.2f GB (%.2f MB)", fileSizeGB, fileSizeMB)
	}

	s.logger.Info("Uploading to S3",
		zap.String("local_path", filePath),
		zap.String("s3_key", s3Key),
		zap.String("bucket", s.bucket),
		zap.Int64("size_bytes", fileSizeBytes),
		zap.String("file_size", fileSizeStr))

	file, err := os.Open(filePath)
	if err != nil {
		return fmt.Errorf("failed to open file for upload: %w", err)
	}
	defer file.Close()

	// Create a progress reader to track upload
	progressR := &progressReader{
		reader:        file,
		totalSize:     fileInfo.Size(),
		bytesRead:     0,
		lastLoggedPct: 0,
		logger:        s.logger,
		s3Key:         s3Key,
	}

	// Track upload start time
	startTime := time.Now()

	_, err = s.client.PutObject(ctx, &s3.PutObjectInput{
		Bucket:        aws.String(s.bucket),
		Key:           aws.String(s3Key),
		Body:          progressR,
		ContentLength: aws.Int64(fileInfo.Size()),
	})
	if err != nil {
		return fmt.Errorf("failed to upload to S3: %w", err)
	}

	// Calculate duration and transfer speed
	duration := time.Since(startTime)
	bytesPerSec := float64(fileInfo.Size()) / duration.Seconds()

	s.logger.Info("Successfully uploaded to S3",
		zap.String("s3_key", s3Key),
		zap.String("bucket", s.bucket),
		zap.Duration("duration", duration),
		zap.Float64("mb_per_sec", bytesPerSec/1024/1024),
		zap.Int64("size_bytes", fileInfo.Size()))

	return nil
}

// DownloadFile downloads a file from S3/Backblaze
func (s *S3Client) DownloadFile(ctx context.Context, s3Key, localPath string) error {
	s.logger.Info("Downloading from S3",
		zap.String("s3_key", s3Key),
		zap.String("local_path", localPath),
		zap.String("bucket", s.bucket))

	// Create the file
	file, err := os.Create(localPath)
	if err != nil {
		return fmt.Errorf("failed to create local file: %w", err)
	}
	defer file.Close()

	// Get the object from S3
	result, err := s.client.GetObject(ctx, &s3.GetObjectInput{
		Bucket: aws.String(s.bucket),
		Key:    aws.String(s3Key),
	})
	if err != nil {
		return fmt.Errorf("failed to download from S3: %w", err)
	}
	defer result.Body.Close()

	// Write the body to file
	_, err = io.Copy(file, result.Body)
	if err != nil {
		return fmt.Errorf("failed to write file: %w", err)
	}

	s.logger.Info("Successfully downloaded from S3",
		zap.String("s3_key", s3Key),
		zap.String("local_path", localPath))

	return nil
}

// ListBackups lists all backups in a directory
func (s *S3Client) ListBackups(ctx context.Context, prefix string) ([]string, error) {
	s.logger.Info("Listing backups", zap.String("prefix", prefix))

	var backups []string
	var continuationToken *string

	for {
		result, err := s.client.ListObjectsV2(ctx, &s3.ListObjectsV2Input{
			Bucket:            aws.String(s.bucket),
			Prefix:            aws.String(prefix),
			ContinuationToken: continuationToken,
		})
		if err != nil {
			return nil, fmt.Errorf("failed to list objects: %w", err)
		}

		for _, item := range result.Contents {
			backups = append(backups, *item.Key)
		}

		if result.IsTruncated == nil || !*result.IsTruncated {
			break
		}
		continuationToken = result.NextContinuationToken
	}

	return backups, nil
}
