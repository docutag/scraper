package storage

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"path/filepath"
	"strings"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/credentials"
	"github.com/aws/aws-sdk-go-v2/service/s3"
)

// S3Config contains S3 storage configuration
type S3Config struct {
	Endpoint        string // Optional: Custom endpoint for MinIO or DigitalOcean Spaces
	Region          string // AWS region or DO region (e.g., "us-east-1" or "sfo3")
	Bucket          string // S3 bucket name
	AccessKeyID     string // AWS access key ID
	SecretAccessKey string // AWS secret access key
	UsePathStyle    bool   // Use path-style addressing (required for MinIO)
}

// S3Storage handles S3-compatible object storage operations
type S3Storage struct {
	client *s3.Client
	bucket string
	config S3Config
}

// NewS3Storage creates a new S3Storage instance
func NewS3Storage(ctx context.Context, cfg S3Config) (*S3Storage, error) {
	if cfg.Bucket == "" {
		return nil, fmt.Errorf("S3 bucket name is required")
	}
	if cfg.Region == "" {
		return nil, fmt.Errorf("S3 region is required")
	}
	if cfg.AccessKeyID == "" || cfg.SecretAccessKey == "" {
		return nil, fmt.Errorf("S3 credentials are required")
	}

	// Build AWS config
	var opts []func(*config.LoadOptions) error

	opts = append(opts, config.WithRegion(cfg.Region))
	opts = append(opts, config.WithCredentialsProvider(
		credentials.NewStaticCredentialsProvider(cfg.AccessKeyID, cfg.SecretAccessKey, ""),
	))

	awsConfig, err := config.LoadDefaultConfig(ctx, opts...)
	if err != nil {
		return nil, fmt.Errorf("failed to load AWS config: %w", err)
	}

	// Create S3 client with custom options
	s3Opts := func(o *s3.Options) {
		if cfg.Endpoint != "" {
			o.BaseEndpoint = aws.String(cfg.Endpoint)
		}
		o.UsePathStyle = cfg.UsePathStyle
	}

	client := s3.NewFromConfig(awsConfig, s3Opts)

	return &S3Storage{
		client: client,
		bucket: cfg.Bucket,
		config: cfg,
	}, nil
}

// SaveImage saves an image to S3
// Returns the S3 key (path within bucket)
func (s *S3Storage) SaveImage(imageData []byte, slug, contentType string) (string, error) {
	// Determine file extension from content type
	ext := extensionFromContentType(contentType)
	if ext == "" {
		ext = ".jpg" // Default extension
	}

	// Generate S3 key: images/YYYY/MM/slug.ext
	now := time.Now()
	year := fmt.Sprintf("%04d", now.Year())
	month := fmt.Sprintf("%02d", int(now.Month()))

	key := filepath.Join("images", year, month, slug+ext)
	// Normalize path separators for S3 (always use forward slashes)
	key = strings.ReplaceAll(key, "\\", "/")

	// Upload to S3
	ctx := context.Background()
	_, err := s.client.PutObject(ctx, &s3.PutObjectInput{
		Bucket:      aws.String(s.bucket),
		Key:         aws.String(key),
		Body:        bytes.NewReader(imageData),
		ContentType: aws.String(contentType),
	})
	if err != nil {
		return "", fmt.Errorf("failed to upload image to S3: %w", err)
	}

	return key, nil
}

// SaveContent saves scraped HTML content to S3
// Returns the S3 key (path within bucket)
func (s *S3Storage) SaveContent(content, slug string) (string, error) {
	// Generate S3 key: content/YYYY/MM/slug.html
	now := time.Now()
	year := fmt.Sprintf("%04d", now.Year())
	month := fmt.Sprintf("%02d", int(now.Month()))

	key := filepath.Join("content", year, month, slug+".html")
	// Normalize path separators for S3 (always use forward slashes)
	key = strings.ReplaceAll(key, "\\", "/")

	// Upload to S3
	ctx := context.Background()
	_, err := s.client.PutObject(ctx, &s3.PutObjectInput{
		Bucket:      aws.String(s.bucket),
		Key:         aws.String(key),
		Body:        bytes.NewReader([]byte(content)),
		ContentType: aws.String("text/html; charset=utf-8"),
	})
	if err != nil {
		return "", fmt.Errorf("failed to upload content to S3: %w", err)
	}

	return key, nil
}

// ReadImage reads an image from S3
func (s *S3Storage) ReadImage(key string) ([]byte, error) {
	ctx := context.Background()
	result, err := s.client.GetObject(ctx, &s3.GetObjectInput{
		Bucket: aws.String(s.bucket),
		Key:    aws.String(key),
	})
	if err != nil {
		return nil, fmt.Errorf("failed to get image from S3: %w", err)
	}
	defer result.Body.Close()

	data, err := io.ReadAll(result.Body)
	if err != nil {
		return nil, fmt.Errorf("failed to read image data from S3: %w", err)
	}

	return data, nil
}

// ReadContent reads content from S3
func (s *S3Storage) ReadContent(key string) (string, error) {
	ctx := context.Background()
	result, err := s.client.GetObject(ctx, &s3.GetObjectInput{
		Bucket: aws.String(s.bucket),
		Key:    aws.String(key),
	})
	if err != nil {
		return "", fmt.Errorf("failed to get content from S3: %w", err)
	}
	defer result.Body.Close()

	data, err := io.ReadAll(result.Body)
	if err != nil {
		return "", fmt.Errorf("failed to read content data from S3: %w", err)
	}

	return string(data), nil
}

// DeleteImage deletes an image from S3
func (s *S3Storage) DeleteImage(key string) error {
	ctx := context.Background()
	_, err := s.client.DeleteObject(ctx, &s3.DeleteObjectInput{
		Bucket: aws.String(s.bucket),
		Key:    aws.String(key),
	})
	if err != nil {
		return fmt.Errorf("failed to delete image from S3: %w", err)
	}

	return nil
}

// DeleteContent deletes content from S3
func (s *S3Storage) DeleteContent(key string) error {
	ctx := context.Background()
	_, err := s.client.DeleteObject(ctx, &s3.DeleteObjectInput{
		Bucket: aws.String(s.bucket),
		Key:    aws.String(key),
	})
	if err != nil {
		return fmt.Errorf("failed to delete content from S3: %w", err)
	}

	return nil
}

// GetFullPath returns the S3 URL for a key
// This returns the key itself for S3, as it's already the full path
func (s *S3Storage) GetFullPath(key string) string {
	// For S3, the "full path" is just the key
	// You could construct a full URL here if needed:
	// return fmt.Sprintf("s3://%s/%s", s.bucket, key)
	return key
}
