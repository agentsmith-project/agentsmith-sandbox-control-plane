package storage

import (
	"context"
	"fmt"
	"io"
	"log"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/minio/minio-go/v7"
	"github.com/minio/minio-go/v7/pkg/credentials"
)

// validSandboxIDPattern matches safe sandboxID values: alphanumeric, hyphens,
// underscores, and dots. Max 256 chars. No path separators allowed.
var validSandboxIDPattern = regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9._-]{0,255}$`)

// sanitizeSandboxID validates and returns the sandboxID for use in storage keys.
// Returns an error if the sandboxID contains path traversal or invalid characters.
func sanitizeSandboxID(sandboxID string) (string, error) {
	if sandboxID == "" {
		return "", fmt.Errorf("sandboxID cannot be empty")
	}
	if strings.Contains(sandboxID, "/") || strings.Contains(sandboxID, "\\") {
		return "", fmt.Errorf("sandboxID contains path separator: %q", sandboxID)
	}
	if strings.Contains(sandboxID, "..") {
		return "", fmt.Errorf("sandboxID contains path traversal: %q", sandboxID)
	}
	if !validSandboxIDPattern.MatchString(sandboxID) {
		return "", fmt.Errorf("sandboxID contains invalid characters: %q", sandboxID)
	}
	return sandboxID, nil
}

// DefaultOperationTimeout is the default timeout for individual MinIO operations.
// If the caller's context has a shorter deadline, the caller's deadline wins.
const DefaultOperationTimeout = 5 * time.Minute

type Client struct {
	client           *minio.Client
	bucket           string
	operationTimeout time.Duration
}

// withTimeout wraps the given context with an operation timeout.
// If the context already has a shorter deadline, it is returned as-is.
func (c *Client) withTimeout(ctx context.Context) (context.Context, context.CancelFunc) {
	timeout := c.operationTimeout
	if timeout == 0 {
		timeout = DefaultOperationTimeout
	}
	return context.WithTimeout(ctx, timeout)
}

func NewClient(endpoint, accessKey, secretKey, bucket string, useSSL bool) (*Client, error) {
	client, err := minio.New(endpoint, &minio.Options{
		Creds:  credentials.NewStaticV4(accessKey, secretKey, ""),
		Secure: useSSL,
	})
	if err != nil {
		return nil, fmt.Errorf("failed to create MinIO client: %w", err)
	}

	// Ensure bucket exists
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	exists, err := client.BucketExists(ctx, bucket)
	if err != nil {
		return nil, fmt.Errorf("failed to check bucket: %w", err)
	}
	if !exists {
		err = client.MakeBucket(ctx, bucket, minio.MakeBucketOptions{})
		if err != nil {
			return nil, fmt.Errorf("failed to create bucket: %w", err)
		}
	}

	return &Client{
		client: client,
		bucket: bucket,
	}, nil
}

// NewClientWithCreds creates a storage client using a Credentials struct
func NewClientWithCreds(creds *Credentials) (*Client, error) {
	return NewClient(creds.Endpoint, creds.AccessKey, creds.SecretKey, creds.Bucket, creds.UseSSL)
}

// GenerateSnapshotKey generates a storage key for a new snapshot.
// Each snapshot is keyed by sandboxID + a UTC timestamp, so multiple
// snapshots (one per pod lifecycle / session) can coexist.
//
// Key format: snapshots/{sandboxID}/{timestamp}.tar.gz
//
// Returns an error if sandboxID contains path traversal or invalid characters.
func (c *Client) GenerateSnapshotKey(sandboxID string) (string, error) {
	safe, err := sanitizeSandboxID(sandboxID)
	if err != nil {
		return "", fmt.Errorf("invalid sandboxID for snapshot key: %w", err)
	}
	ts := time.Now().UTC().Format("20060102T150405Z")
	return fmt.Sprintf("snapshots/%s/%s.tar.gz", safe, ts), nil
}

// GetLatestSnapshotKey returns the key of the most recent snapshot for a sandbox.
// Returns ("", nil) if no snapshots exist (sandbox expired or never created).
func (c *Client) GetLatestSnapshotKey(ctx context.Context, sandboxID string) (string, error) {
	safe, err := sanitizeSandboxID(sandboxID)
	if err != nil {
		return "", fmt.Errorf("invalid sandboxID for snapshot lookup: %w", err)
	}
	prefix := fmt.Sprintf("snapshots/%s/", safe)

	var keys []string
	for obj := range c.client.ListObjects(ctx, c.bucket, minio.ListObjectsOptions{
		Prefix:    prefix,
		Recursive: true,
	}) {
		if obj.Err != nil {
			return "", fmt.Errorf("failed to list snapshots for sandbox %s: %w", sandboxID, obj.Err)
		}
		// Only include .tar.gz files
		if strings.HasSuffix(obj.Key, ".tar.gz") {
			keys = append(keys, obj.Key)
		}
	}

	if len(keys) == 0 {
		return "", nil
	}

	// Keys are timestamp-sorted lexicographically, pick the latest
	sort.Strings(keys)
	return keys[len(keys)-1], nil
}

// SnapshotExists checks if any snapshot exists for the given sandbox.
func (c *Client) SnapshotExists(ctx context.Context, sandboxID string) (bool, error) {
	key, err := c.GetLatestSnapshotKey(ctx, sandboxID)
	if err != nil {
		return false, err
	}
	return key != "", nil
}

func (c *Client) UploadSnapshot(ctx context.Context, key string, data io.Reader, size int64) error {
	opCtx, cancel := c.withTimeout(ctx)
	defer cancel()

	_, err := c.client.PutObject(opCtx, c.bucket, key, data, size, minio.PutObjectOptions{
		ContentType: "application/gzip",
	})
	if err != nil {
		return fmt.Errorf("failed to upload snapshot: %w", err)
	}
	return nil
}

func (c *Client) DownloadSnapshot(ctx context.Context, key string) (io.ReadCloser, int64, error) {
	opCtx, cancel := c.withTimeout(ctx)

	obj, err := c.client.GetObject(opCtx, c.bucket, key, minio.GetObjectOptions{})
	if err != nil {
		cancel()
		return nil, 0, fmt.Errorf("failed to get snapshot: %w", err)
	}

	info, err := obj.Stat()
	if err != nil {
		obj.Close()
		cancel()
		return nil, 0, fmt.Errorf("failed to stat snapshot: %w", err)
	}

	// Wrap the object in a reader that calls cancel() when closed,
	// ensuring the timeout context is always cleaned up.
	wrapped := &cancelOnClose{ReadCloser: obj, cancel: cancel}
	return wrapped, info.Size, nil
}

// cancelOnClose wraps a ReadCloser and calls a cancel function when closed.
type cancelOnClose struct {
	io.ReadCloser
	cancel context.CancelFunc
}

func (c *cancelOnClose) Close() error {
	err := c.ReadCloser.Close()
	c.cancel()
	return err
}

// DownloadLatestSnapshot downloads the most recent snapshot for a sandbox.
// Returns (nil, 0, nil) if no snapshot exists.
func (c *Client) DownloadLatestSnapshot(ctx context.Context, sandboxID string) (io.ReadCloser, int64, error) {
	key, err := c.GetLatestSnapshotKey(ctx, sandboxID)
	if err != nil {
		return nil, 0, err
	}
	if key == "" {
		return nil, 0, nil
	}

	log.Printf("Storage: downloading latest snapshot for sandbox %s: %s", sandboxID, key)
	return c.DownloadSnapshot(ctx, key)
}

func (c *Client) DeleteSnapshot(ctx context.Context, key string) error {
	err := c.client.RemoveObject(ctx, c.bucket, key, minio.RemoveObjectOptions{})
	if err != nil {
		return fmt.Errorf("failed to delete snapshot %s: %w", key, err)
	}
	return nil
}
