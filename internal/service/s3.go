package service

import (
	"context"
	"io"
	"log/slog"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/credentials"
	"github.com/aws/aws-sdk-go-v2/feature/s3/transfermanager"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/oszuidwest/zwfm-aerontoolbox/internal/config"
	"github.com/oszuidwest/zwfm-aerontoolbox/internal/types"
)

type backupObjectStore interface {
	upload(ctx context.Context, filename string, body io.Reader) error
	delete(ctx context.Context, filename string) error
}

// s3Service syncs managed backup files to S3-compatible storage.
type s3Service struct {
	tm     *transfermanager.Client
	client *s3.Client
	bucket string
	prefix string
}

var _ backupObjectStore = (*s3Service)(nil)

// newS3Service builds an S3 sync client for the configured bucket. Callers gate
// on S3 being enabled; this always returns a live client so a disabled store can
// never be boxed into the backupObjectStore interface as a non-nil typed nil.
func newS3Service(cfg *config.S3Config) *s3Service {
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
	}
}

// ptrOrNil returns nil for an empty string and an AWS string pointer otherwise.
func ptrOrNil(s string) *string {
	if s == "" {
		return nil
	}
	return aws.String(s)
}

// upload streams one backup file to remote storage.
func (s *s3Service) upload(ctx context.Context, filename string, body io.Reader) error {
	key := s.prefix + filename
	start := time.Now()

	_, err := s.tm.UploadObject(ctx, &transfermanager.UploadObjectInput{
		Bucket: aws.String(s.bucket),
		Key:    aws.String(key),
		Body:   body,
	})
	if err != nil {
		return types.NewOperationError("S3 upload", err)
	}

	slog.Info("Backup uploaded to S3",
		"key", key,
		"duration", time.Since(start).Round(time.Millisecond))

	return nil
}

// delete removes one backup object from remote storage.
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
