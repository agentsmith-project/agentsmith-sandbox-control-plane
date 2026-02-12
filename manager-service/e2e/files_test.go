//go:build e2e

package e2e

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"io"
	"net/http"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// buildTarGz creates an in-memory tar.gz archive containing a single file
// with the given name and content.
func buildTarGz(t *testing.T, fileName string, content []byte) *bytes.Buffer {
	t.Helper()

	var buf bytes.Buffer
	gw := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gw)

	hdr := &tar.Header{
		Name: fileName,
		Mode: 0644,
		Size: int64(len(content)),
	}
	require.NoError(t, tw.WriteHeader(hdr))
	_, err := tw.Write(content)
	require.NoError(t, err)

	require.NoError(t, tw.Close())
	require.NoError(t, gw.Close())

	return &buf
}

// TestE2E_UploadAndDownload uploads a tar.gz archive into a sandbox, then
// downloads from the same path and verifies the response looks correct.
func TestE2E_UploadAndDownload(t *testing.T) {
	c := newTestClient(t)
	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
	defer cancel()

	sessionID := randomSessionID()

	// Create sandbox
	createResp := c.createSandbox(ctx, t, sessionID, createSandboxRequest{TTLSeconds: 300})
	createResp.Body.Close()
	require.Equal(t, http.StatusOK, createResp.StatusCode)
	defer func() {
		cleanResp := c.deleteSandbox(ctx, t, sessionID)
		cleanResp.Body.Close()
	}()

	// Build a small tar.gz with a test file
	fileContent := []byte("hello from e2e upload test\n")
	archive := buildTarGz(t, "test-upload.txt", fileContent)

	// Upload
	dest := "/workspace"
	uploadResp := c.uploadFile(ctx, t, sessionID, dest, archive)
	defer uploadResp.Body.Close()

	require.Equal(t, http.StatusOK, uploadResp.StatusCode, "upload should return 200")

	// Download from the same path
	downloadResp := c.downloadFile(ctx, t, sessionID, dest)
	defer downloadResp.Body.Close()

	require.Equal(t, http.StatusOK, downloadResp.StatusCode, "download should return 200")

	contentType := downloadResp.Header.Get("Content-Type")
	assert.Contains(t, contentType, "application/x-gzip",
		"download Content-Type should be application/x-gzip")

	body, err := io.ReadAll(downloadResp.Body)
	require.NoError(t, err)
	assert.NotEmpty(t, body, "downloaded body should not be empty")
}
