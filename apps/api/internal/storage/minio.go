// Package storage wraps MinIO for PaperLess file operations.
// All methods permission-check at the caller layer — this package only does IO.
package storage

import (
	"context"
	"fmt"
	"io"

	"github.com/minio/minio-go/v7"
	"github.com/minio/minio-go/v7/pkg/credentials"

	"paperless-api/internal/config"
)

// Client is the PaperLess MinIO client.
type Client struct {
	mc     *minio.Client
	bucket string
}

// New connects to MinIO and ensures the configured bucket exists.
func New(cfg *config.Config) (*Client, error) {
	mc, err := minio.New(cfg.Storage.Endpoint, &minio.Options{
		Creds:  credentials.NewStaticV4(cfg.Storage.AccessKey, cfg.Storage.SecretKey, ""),
		Secure: cfg.Storage.UseSSL,
	})
	if err != nil {
		return nil, fmt.Errorf("minio client: %w", err)
	}
	return &Client{mc: mc, bucket: cfg.Storage.Bucket}, nil
}

// EnsureBucket creates the bucket if it does not exist.
func (c *Client) EnsureBucket(ctx context.Context) error {
	exists, err := c.mc.BucketExists(ctx, c.bucket)
	if err != nil {
		return fmt.Errorf("bucket exists check: %w", err)
	}
	if !exists {
		if err := c.mc.MakeBucket(ctx, c.bucket, minio.MakeBucketOptions{}); err != nil {
			return fmt.Errorf("make bucket %s: %w", c.bucket, err)
		}
	}
	return nil
}

// Put stores an object. objectKey is the full path within the bucket.
func (c *Client) Put(ctx context.Context, objectKey, contentType string, r io.Reader, size int64) error {
	_, err := c.mc.PutObject(ctx, c.bucket, objectKey, r, size, minio.PutObjectOptions{
		ContentType: contentType,
	})
	if err != nil {
		return fmt.Errorf("put %s: %w", objectKey, err)
	}
	return nil
}

// Get opens an object for reading. Caller must close the returned ReadCloser.
func (c *Client) Get(ctx context.Context, objectKey string) (io.ReadCloser, int64, error) {
	obj, err := c.mc.GetObject(ctx, c.bucket, objectKey, minio.GetObjectOptions{})
	if err != nil {
		return nil, 0, fmt.Errorf("get %s: %w", objectKey, err)
	}
	info, err := obj.Stat()
	if err != nil {
		obj.Close()
		return nil, 0, fmt.Errorf("stat %s: %w", objectKey, err)
	}
	return obj, info.Size, nil
}

// Delete removes an object.
func (c *Client) Delete(ctx context.Context, objectKey string) error {
	return c.mc.RemoveObject(ctx, c.bucket, objectKey, minio.RemoveObjectOptions{})
}

// Ping verifies MinIO is reachable AND the configured bucket exists. Both are
// required for the service to function — uploads and PDF downloads all target
// this bucket. Returns an error if the server is unreachable, credentials are
// rejected, or the bucket is missing.
//
// IMPORTANT: BucketExists returns (false, nil) — NOT an error — when the server
// is reachable but the bucket does not exist (e.g. MinIO restarted with a fresh
// volume). A bare error check would report healthy in that case while every
// file operation fails. We must treat a missing bucket as unhealthy explicitly.
func (c *Client) Ping(ctx context.Context) error {
	exists, err := c.mc.BucketExists(ctx, c.bucket)
	if err != nil {
		return fmt.Errorf("minio ping: %w", err)
	}
	if !exists {
		return fmt.Errorf("minio ping: bucket %q does not exist", c.bucket)
	}
	return nil
}
