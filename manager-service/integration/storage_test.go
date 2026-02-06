//go:build Integration
// +build Integration

package integration_test

import (
	"bytes"
	"context"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/sandbox/manager/internal/storage"
)

func TestStorage_MinIOIntegration(t *testing.T) {
	ctx := context.Background()

	// Get MinIO configuration from environment
	endpoint := os.Getenv("MINIO_ENDPOINT")
	if endpoint == "" {
		endpoint = "http://localhost:9000"
	}

	accessKey := os.Getenv("MINIO_ACCESS_KEY")
	if accessKey == "" {
		accessKey = "minioadmin"
	}

	secretKey := os.Getenv("MINIO_SECRET_KEY")
	if secretKey == "" {
		secretKey = "minioadmin"
	}

	bucket := os.Getenv("MINIO_BUCKET")
	if bucket == "" {
		bucket = "sandbox-snapshots"
	}

	// Create storage client
	client, err := storage.NewClient(endpoint, accessKey, secretKey, bucket, false)
	require.NoError(t, err, "Failed to create storage client")

	t.Run("Upload and Download Snapshot", func(t *testing.T) {
		sessionID := "test-session-" + time.Now().Format("20060102150405")
		testData := []byte("test snapshot data for integration test")
		key := "test-snapshots/" + sessionID + ".tar.gz"

		// Upload
		err := client.UploadSnapshot(ctx, key, bytes.NewReader(testData), int64(len(testData)))
		require.NoError(t, err, "Failed to upload snapshot")

		// Download
		rc, size, err := client.DownloadSnapshot(ctx, key)
		require.NoError(t, err, "Failed to download snapshot")
		defer rc.Close()

		assert.Equal(t, int64(len(testData)), size, "Downloaded size should match uploaded size")

		downloaded := make([]byte, size)
		n, err := rc.Read(downloaded)
		require.NoError(t, err, "Failed to read downloaded data")
		require.Equal(t, len(testData), n, "Downloaded bytes should match")
		assert.Equal(t, testData, downloaded, "Downloaded data doesn't match uploaded data")

		// Cleanup
		_ = client.DeleteSnapshot(ctx, key)
	})

	t.Run("Snapshot Exists", func(t *testing.T) {
		sessionID := "test-exists-" + time.Now().Format("20060102150405")
		testData := []byte("test data for exists check")
		key := "test-snapshots/" + sessionID + ".tar.gz"

		// Initially should not exist
		exists, err := client.SnapshotExists(ctx, key)
		require.NoError(t, err, "Failed to check existence")
		assert.False(t, exists, "Snapshot should not exist initially")

		// Upload
		err = client.UploadSnapshot(ctx, key, bytes.NewReader(testData), int64(len(testData)))
		require.NoError(t, err)

		// Now should exist
		exists, err = client.SnapshotExists(ctx, key)
		require.NoError(t, err)
		assert.True(t, exists, "Snapshot should exist after upload")

		// Cleanup
		_ = client.DeleteSnapshot(ctx, key)

		// Should not exist after delete
		exists, err = client.SnapshotExists(ctx, key)
		require.NoError(t, err)
		assert.False(t, exists, "Snapshot should not exist after delete")
	})

	t.Run("Delete Snapshot", func(t *testing.T) {
		sessionID := "test-delete-" + time.Now().Format("20060102150405")
		key := "test-snapshots/" + sessionID + ".tar.gz"
		testData := []byte("test data for delete")

		// Upload
		err := client.UploadSnapshot(ctx, key, bytes.NewReader(testData), int64(len(testData)))
		require.NoError(t, err)

		// Verify exists
		exists, _ := client.SnapshotExists(ctx, key)
		assert.True(t, exists, "Snapshot should exist before delete")

		// Delete
		err = client.DeleteSnapshot(ctx, key)
		require.NoError(t, err, "Failed to delete snapshot")

		// Verify gone
		exists, _ = client.SnapshotExists(ctx, key)
		assert.False(t, exists, "Snapshot should not exist after delete")
	})

	t.Run("Generate Snapshot Key", func(t *testing.T) {
		workspaceID := "ws_abc123"
		projectID := "proj_xyz789"
		agentThreadID := "at_thread456"

		key := client.GenerateSnapshotKey(workspaceID, projectID, agentThreadID)

		// Key should have prefixes trimmed
		assert.NotContains(t, key, "ws_", "Workspace prefix should be trimmed")
		assert.NotContains(t, key, "proj_", "Project prefix should be trimmed")
		assert.NotContains(t, key, "at_", "Agent thread prefix should be trimmed")

		// Key should have proper structure
		assert.True(t, strings.HasPrefix(key, "snapshots/"), "Key should start with snapshots/")
		assert.True(t, strings.HasSuffix(key, "/workspace.tar.gz"), "Key should end with workspace.tar.gz")

		expectedParts := []string{"abc123", "xyz789", "thread456"}
		for _, part := range expectedParts {
			assert.Contains(t, key, part, "Key should contain ID: %s", part)
		}
	})

	t.Run("Multiple Upload Download Cycle", func(t *testing.T) {
		// Test multiple operations in sequence
		cycles := 3
		for i := 0; i < cycles; i++ {
			sessionID := "test-cycle-" + time.Now().Format("20060102150405") + "-" + string(rune('a'+i))
			key := "test-snapshots/" + sessionID + ".tar.gz"
			testData := []byte("cycle data " + string(rune('0'+i)))

			// Upload
			err := client.UploadSnapshot(ctx, key, bytes.NewReader(testData), int64(len(testData)))
			require.NoError(t, err, "Cycle %d: upload failed", i)

			// Download
			rc, size, err := client.DownloadSnapshot(ctx, key)
			require.NoError(t, err, "Cycle %d: download failed", i)

			downloaded := make([]byte, size)
			n, err := rc.Read(downloaded)
			rc.Close()
			require.NoError(t, err, "Cycle %d: read failed", i)
			assert.Equal(t, testData, downloaded[:n], "Cycle %d: data mismatch", i)

			// Cleanup
			_ = client.DeleteSnapshot(ctx, key)
		}
	})
}
