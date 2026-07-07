// Package storage wraps the S3-compatible object store (MinIO) used to hold
// uploaded inputs and converted outputs.
package storage

import (
	"context"
	"fmt"
	"io"
	"os"
	"time"

	"github.com/minio/minio-go/v7"
	"github.com/minio/minio-go/v7/pkg/credentials"
	"github.com/minio/minio-go/v7/pkg/lifecycle"
)

// Client is a thin wrapper over the MinIO SDK bound to a single bucket.
type Client struct {
	mc     *minio.Client
	bucket string
}

// New builds a storage client from S3_* environment variables and verifies the
// configured bucket exists.
func New(ctx context.Context) (*Client, error) {
	endpoint := os.Getenv("S3_ENDPOINT")
	accessKey := os.Getenv("S3_ACCESS_KEY")
	secretKey := os.Getenv("S3_SECRET_KEY")
	bucket := os.Getenv("S3_BUCKET")
	useSSL := os.Getenv("S3_USE_SSL") == "true"

	if endpoint == "" || accessKey == "" || secretKey == "" || bucket == "" {
		return nil, fmt.Errorf("S3_ENDPOINT, S3_ACCESS_KEY, S3_SECRET_KEY and S3_BUCKET must be set")
	}

	mc, err := minio.New(endpoint, &minio.Options{
		Creds:  credentials.NewStaticV4(accessKey, secretKey, ""),
		Secure: useSSL,
	})
	if err != nil {
		return nil, fmt.Errorf("init minio client: %w", err)
	}

	exists, err := mc.BucketExists(ctx, bucket)
	if err != nil {
		return nil, fmt.Errorf("check bucket %q: %w", bucket, err)
	}
	if !exists {
		return nil, fmt.Errorf("bucket %q does not exist", bucket)
	}

	return &Client{mc: mc, bucket: bucket}, nil
}

// Upload stores an object under key. If size is negative the object is streamed
// with an unknown length.
func (c *Client) Upload(ctx context.Context, key string, r io.Reader, size int64, contentType string) error {
	_, err := c.mc.PutObject(ctx, c.bucket, key, r, size, minio.PutObjectOptions{
		ContentType: contentType,
	})
	if err != nil {
		return fmt.Errorf("upload %q: %w", key, err)
	}
	return nil
}

// Download opens an object for reading. The caller must close the returned reader.
func (c *Client) Download(ctx context.Context, key string) (io.ReadCloser, error) {
	obj, err := c.mc.GetObject(ctx, c.bucket, key, minio.GetObjectOptions{})
	if err != nil {
		return nil, fmt.Errorf("download %q: %w", key, err)
	}
	// GetObject is lazy; probe with Stat so a missing object fails here rather
	// than on first Read.
	if _, err := obj.Stat(); err != nil {
		_ = obj.Close()
		return nil, fmt.Errorf("stat %q: %w", key, err)
	}
	return obj, nil
}

// PresignGet returns a time-limited URL for downloading an object directly.
func (c *Client) PresignGet(ctx context.Context, key string, ttl time.Duration) (string, error) {
	u, err := c.mc.PresignedGetObject(ctx, c.bucket, key, ttl, nil)
	if err != nil {
		return "", fmt.Errorf("presign %q: %w", key, err)
	}
	return u.String(), nil
}

// lifecycleConfig builds the bucket lifecycle rules that expire objects under
// the uploads/ and results/ prefixes (see keys.go) after ttl. MinIO's
// Expiration.Days field only has whole-day granularity, so ttl is rounded
// down to a day count and clamped up to a minimum of 1 day — a 0-day rule
// can never be emitted (STOR-01, D-10/D-11).
func lifecycleConfig(ttl time.Duration) *lifecycle.Configuration {
	days := int(ttl.Hours() / 24)
	if days < 1 {
		days = 1
	}
	cfg := lifecycle.NewConfiguration()
	cfg.Rules = []lifecycle.Rule{
		{
			ID:         "octoconv-uploads-ttl",
			Status:     "Enabled",
			RuleFilter: lifecycle.Filter{Prefix: "uploads/"},
			Expiration: lifecycle.Expiration{Days: lifecycle.ExpirationDays(days)},
		},
		{
			ID:         "octoconv-results-ttl",
			Status:     "Enabled",
			RuleFilter: lifecycle.Filter{Prefix: "results/"},
			Expiration: lifecycle.Expiration{Days: lifecycle.ExpirationDays(days)},
		},
	}
	return cfg
}

// EnsureLifecycle applies a bucket lifecycle rule that expires objects under
// uploads/ and results/ after ttl. SetBucketLifecycle is a full-document PUT
// on the server side, so this is idempotent and safe to call on every
// process startup (D-12).
func (c *Client) EnsureLifecycle(ctx context.Context, ttl time.Duration) error {
	if err := c.mc.SetBucketLifecycle(ctx, c.bucket, lifecycleConfig(ttl)); err != nil {
		return fmt.Errorf("set bucket lifecycle: %w", err)
	}
	return nil
}

// Ping is a lightweight, read-only probe of the configured bucket for the
// health endpoint (D-16). It never writes or reads object data.
func (c *Client) Ping(ctx context.Context) error {
	if _, err := c.mc.BucketExists(ctx, c.bucket); err != nil {
		return fmt.Errorf("ping bucket %q: %w", c.bucket, err)
	}
	return nil
}
