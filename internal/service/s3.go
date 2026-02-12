package service

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/credentials"
	"github.com/aws/aws-sdk-go-v2/feature/s3/transfermanager"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/oszuidwest/zwfm-aerontoolbox/internal/config"
	"github.com/oszuidwest/zwfm-aerontoolbox/internal/types"
)

// s3Service manages uploads and deletions of backup files to S3-compatible storage.
type s3Service struct {
	tm     *transfermanager.Client
	client *s3.Client
	bucket string
	prefix string
}

// newS3Service creates an S3 client for backup synchronization, or returns nil if disabled.
func newS3Service(cfg *config.S3Config) (*s3Service, error) { //nolint:unparam // error return kept for future use
	if !cfg.Enabled {
		return nil, nil
	}

	client := s3.New(s3.Options{
		Region:       cfg.Region,
		BaseEndpoint: ptrOrNil(cfg.Endpoint),
		UsePathStyle: cfg.ForcePathStyle,
		Credentials: credentials.NewStaticCredentialsProvider(
			cfg.AccessKeyID,
			cfg.SecretAccessKey,
			"",
		),
	})

	slog.Info("S3 sync enabled",
		"bucket", cfg.Bucket,
		"region", cfg.Region,
		"endpoint", cfg.Endpoint,
		"prefix", cfg.GetPathPrefix())

	return &s3Service{
		tm:     transfermanager.New(client),
		client: client,
		bucket: cfg.Bucket,
		prefix: cfg.GetPathPrefix(),
	}, nil
}

// ptrOrNil returns nil for empty strings, otherwise a pointer to the string.
func ptrOrNil(s string) *string {
	if s == "" {
		return nil
	}
	return aws.String(s)
}

// upload transfers a backup file to S3 storage.
func (s *s3Service) upload(ctx context.Context, filename, localPath string) (err error) {
	file, err := os.Open(localPath) //nolint:gosec // localPath is constructed from validated backup filename
	if err != nil {
		return types.NewOperationError("S3 upload", fmt.Errorf("open file: %w", err))
	}
	defer func() {
		if closeErr := file.Close(); closeErr != nil && err == nil {
			err = types.NewOperationError("S3 upload", fmt.Errorf("close file: %w", closeErr))
		}
	}()

	key := s.prefix + filename
	start := time.Now()

	_, err = s.tm.UploadObject(ctx, &transfermanager.UploadObjectInput{
		Bucket: aws.String(s.bucket),
		Key:    aws.String(key),
		Body:   file,
	})
	if err != nil {
		return types.NewOperationError("S3 upload", err)
	}

	slog.Info("Backup uploaded to S3",
		"key", key,
		"duration", time.Since(start).Round(time.Millisecond))

	return nil
}

// delete removes a backup file from S3 storage.
func (s *s3Service) delete(ctx context.Context, filename string) error {
	key := s.prefix + filename

	_, err := s.client.DeleteObject(ctx, &s3.DeleteObjectInput{
		Bucket: aws.String(s.bucket),
		Key:    aws.String(key),
	})
	if err != nil {
		return types.NewOperationError("S3 delete", err)
	}

	slog.Info("Backup deleted from S3", "key", key)
	return nil
}
