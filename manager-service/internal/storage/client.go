package storage

import (
	"context"
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/minio/minio-go/v7"
	"github.com/minio/minio-go/v7/pkg/credentials"
)

type Client struct {
	client *minio.Client
	bucket string
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

func (c *Client) GenerateSnapshotKey(workspaceID, projectID, agentThreadID string) string {
	return fmt.Sprintf("snapshots/%s/%s/%s/workspace.tar.gz",
		strings.TrimPrefix(workspaceID, "ws_"),
		strings.TrimPrefix(projectID, "proj_"),
		strings.TrimPrefix(agentThreadID, "at_"),
	)
}

func (c *Client) UploadSnapshot(ctx context.Context, key string, data io.Reader, size int64) error {
	_, err := c.client.PutObject(ctx, c.bucket, key, data, size, minio.PutObjectOptions{
		ContentType: "application/gzip",
	})
	if err != nil {
		return fmt.Errorf("failed to upload snapshot: %w", err)
	}
	return nil
}

func (c *Client) DownloadSnapshot(ctx context.Context, key string) (io.ReadCloser, int64, error) {
	obj, err := c.client.GetObject(ctx, c.bucket, key, minio.GetObjectOptions{})
	if err != nil {
		return nil, 0, fmt.Errorf("failed to get snapshot: %w", err)
	}

	info, err := obj.Stat()
	if err != nil {
		obj.Close()
		return nil, 0, fmt.Errorf("failed to stat snapshot: %w", err)
	}

	return obj, info.Size, nil
}

func (c *Client) DeleteSnapshot(ctx context.Context, key string) error {
	return c.client.RemoveObject(ctx, c.bucket, key, minio.RemoveObjectOptions{})
}

func (c *Client) SnapshotExists(ctx context.Context, key string) (bool, error) {
	_, err := c.client.StatObject(ctx, c.bucket, key, minio.StatObjectOptions{})
	if err != nil {
		errResp := minio.ToErrorResponse(err)
		// Check if this is a valid minio error response (Code is set)
		// ToErrorResponse returns a zero-value ErrorResponse for non-minio errors,
		// with Code == "" (empty string), so we check for "NoSuchKey" explicitly
		if errResp.Code == "NoSuchKey" {
			return false, nil
		}
		return false, err
	}
	return true, nil
}
