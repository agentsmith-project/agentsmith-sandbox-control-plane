//go:build integration

package storage

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/minio/minio-go/v7"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// testClient creates a storage Client connected to a real MinIO instance.
// It reads MINIO_ENDPOINT from the environment (default "localhost:9000")
// and creates a unique bucket per test, cleaned up via t.Cleanup.
func testClient(t *testing.T) *Client {
	t.Helper()

	endpoint := os.Getenv("MINIO_ENDPOINT")
	if endpoint == "" {
		endpoint = "localhost:9000"
	}

	bucket := fmt.Sprintf("test-%s", uuid.New().String())

	client, err := NewClient(endpoint, "minioadmin", "minioadmin", bucket, false)
	require.NoError(t, err, "failed to create storage client")

	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()

		// Remove all objects in the bucket before removing the bucket itself.
		objectsCh := client.client.ListObjects(ctx, client.bucket, minio.ListObjectsOptions{Recursive: true})
		for obj := range objectsCh {
			if obj.Err != nil {
				continue
			}
			_ = client.client.RemoveObject(ctx, client.bucket, obj.Key, minio.RemoveObjectOptions{})
		}
		_ = client.client.RemoveBucket(ctx, client.bucket)
	})

	return client
}

func TestIntegration_NewClient(t *testing.T) {
	endpoint := os.Getenv("MINIO_ENDPOINT")
	if endpoint == "" {
		endpoint = "localhost:9000"
	}

	bucket := fmt.Sprintf("test-%s", uuid.New().String())

	client, err := NewClient(endpoint, "minioadmin", "minioadmin", bucket, false)
	assert.NoError(t, err)
	assert.NotNil(t, client)

	// Clean up the bucket we just created.
	if client != nil {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		_ = client.client.RemoveBucket(ctx, client.bucket)
	}
}

func TestIntegration_UploadDownload_RoundTrip(t *testing.T) {
	c := testClient(t)
	ctx := context.Background()

	sandboxID := uuid.New().String()
	key, err := c.GenerateSnapshotKey(sandboxID)
	require.NoError(t, err)

	payload := "hello world — this is snapshot data for round-trip test"
	data := bytes.NewBufferString(payload)

	// Upload
	err := c.UploadSnapshot(ctx, key, data, int64(len(payload)))
	require.NoError(t, err, "upload should succeed")

	// Download
	reader, size, err := c.DownloadSnapshot(ctx, key)
	require.NoError(t, err, "download should succeed")
	defer reader.Close()

	assert.Equal(t, int64(len(payload)), size)

	downloaded, err := io.ReadAll(reader)
	require.NoError(t, err)
	assert.Equal(t, payload, string(downloaded))
}

func TestIntegration_GenerateSnapshotKey_And_GetLatest(t *testing.T) {
	c := testClient(t)
	ctx := context.Background()

	sandboxID := uuid.New().String()

	// Generate first key and upload.
	key1, err := c.GenerateSnapshotKey(sandboxID)
	require.NoError(t, err)
	err = c.UploadSnapshot(ctx, key1, strings.NewReader("snapshot-1"), int64(len("snapshot-1")))
	require.NoError(t, err)

	// Sleep to ensure the second key has a later timestamp.
	time.Sleep(1 * time.Second)

	// Generate second key and upload.
	key2, err := c.GenerateSnapshotKey(sandboxID)
	require.NoError(t, err)
	err = c.UploadSnapshot(ctx, key2, strings.NewReader("snapshot-2"), int64(len("snapshot-2")))
	require.NoError(t, err)

	// The two keys must be different.
	require.NotEqual(t, key1, key2, "keys generated 1s apart should differ")

	// GetLatestSnapshotKey should return the second (newer) key.
	latestKey, err := c.GetLatestSnapshotKey(ctx, sandboxID)
	require.NoError(t, err)
	assert.Equal(t, key2, latestKey, "latest key should be the second upload")
}

func TestIntegration_SnapshotExists(t *testing.T) {
	c := testClient(t)
	ctx := context.Background()

	sandboxID := uuid.New().String()

	// Before any upload, SnapshotExists should return false.
	exists, err := c.SnapshotExists(ctx, sandboxID)
	require.NoError(t, err)
	assert.False(t, exists, "no snapshot uploaded yet")

	// Upload a snapshot.
	key, err := c.GenerateSnapshotKey(sandboxID)
	require.NoError(t, err)
	err = c.UploadSnapshot(ctx, key, strings.NewReader("exists-test"), int64(len("exists-test")))
	require.NoError(t, err)

	// Now SnapshotExists should return true.
	exists, err = c.SnapshotExists(ctx, sandboxID)
	require.NoError(t, err)
	assert.True(t, exists, "snapshot was uploaded")

	// A completely different sandboxID should return false.
	otherID := uuid.New().String()
	exists, err = c.SnapshotExists(ctx, otherID)
	require.NoError(t, err)
	assert.False(t, exists, "no snapshot for other sandbox")
}

func TestIntegration_DownloadLatestSnapshot(t *testing.T) {
	c := testClient(t)
	ctx := context.Background()

	sandboxID := uuid.New().String()

	// Upload first snapshot.
	key1, err := c.GenerateSnapshotKey(sandboxID)
	require.NoError(t, err)
	err = c.UploadSnapshot(ctx, key1, strings.NewReader("first"), int64(len("first")))
	require.NoError(t, err)

	time.Sleep(1 * time.Second)

	// Upload second snapshot.
	key2, err := c.GenerateSnapshotKey(sandboxID)
	require.NoError(t, err)
	err = c.UploadSnapshot(ctx, key2, strings.NewReader("second"), int64(len("second")))
	require.NoError(t, err)

	time.Sleep(1 * time.Second)

	// Upload third snapshot.
	key3, err := c.GenerateSnapshotKey(sandboxID)
	require.NoError(t, err)
	err = c.UploadSnapshot(ctx, key3, strings.NewReader("third"), int64(len("third")))
	require.NoError(t, err)

	// DownloadLatestSnapshot should return the third upload.
	reader, size, err := c.DownloadLatestSnapshot(ctx, sandboxID)
	require.NoError(t, err)
	require.NotNil(t, reader, "reader should not be nil")
	defer reader.Close()

	assert.Equal(t, int64(len("third")), size)

	body, err := io.ReadAll(reader)
	require.NoError(t, err)
	assert.Equal(t, "third", string(body))
}

func TestIntegration_DeleteSnapshot(t *testing.T) {
	c := testClient(t)
	ctx := context.Background()

	sandboxID := uuid.New().String()
	key, err := c.GenerateSnapshotKey(sandboxID)
	require.NoError(t, err)

	// Upload a snapshot.
	err = c.UploadSnapshot(ctx, key, strings.NewReader("delete-me"), int64(len("delete-me")))
	require.NoError(t, err)

	// Verify it exists via download.
	reader, _, err := c.DownloadSnapshot(ctx, key)
	require.NoError(t, err)
	reader.Close()

	// Delete the snapshot.
	err = c.DeleteSnapshot(ctx, key)
	require.NoError(t, err)

	// After deletion, download should fail.
	_, _, err = c.DownloadSnapshot(ctx, key)
	assert.Error(t, err, "download after delete should return an error")

	// SnapshotExists should also return false.
	exists, err := c.SnapshotExists(ctx, sandboxID)
	require.NoError(t, err)
	assert.False(t, exists, "snapshot should not exist after deletion")
}

func TestIntegration_BucketAutoCreation(t *testing.T) {
	endpoint := os.Getenv("MINIO_ENDPOINT")
	if endpoint == "" {
		endpoint = "localhost:9000"
	}

	// Use a unique bucket name that doesn't exist yet.
	bucket := fmt.Sprintf("auto-create-%s", uuid.New().String())

	client, err := NewClient(endpoint, "minioadmin", "minioadmin", bucket, false)
	require.NoError(t, err, "NewClient should auto-create the bucket")
	require.NotNil(t, client)

	// Verify the bucket exists by checking with the underlying minio client.
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	exists, err := client.client.BucketExists(ctx, bucket)
	require.NoError(t, err)
	assert.True(t, exists, "bucket should have been auto-created")

	// Clean up.
	_ = client.client.RemoveBucket(ctx, bucket)
}
