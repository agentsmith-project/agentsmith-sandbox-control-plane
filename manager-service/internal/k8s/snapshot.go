package k8s

import (
	"context"
	"fmt"
	"io"
	"time"
)

// SnapshotWorkspace creates a tar.gz of /workspace
// Uses kubectl exec for streaming (shell-bridge doesn't support stdout streaming yet)
func (c *Client) SnapshotWorkspace(ctx context.Context, namespace, podName string) (io.ReadCloser, error) {
	reader, writer := io.Pipe()

	go func() {
		defer writer.Close()

		// Check if context is already cancelled before starting work
		select {
		case <-ctx.Done():
			writer.CloseWithError(ctx.Err())
			return
		default:
		}

		// Set a timeout for the snapshot operation (2 minutes)
		execCtx, cancel := context.WithTimeout(ctx, 2*time.Minute)
		defer cancel()

		// Execute tar command using kubectl exec (for streaming support)
		err := c.Exec(execCtx, namespace, podName, c.defaultContainer, []string{
			"tar", "czf", "-", "-C", "/workspace", ".",
		}, StreamOptions{
			Stdout: writer,
		})
		if err != nil {
			writer.CloseWithError(fmt.Errorf("tar command failed: %w", err))
			return
		}
	}()

	return reader, nil
}

// RestoreWorkspace extracts tar.gz to /workspace
// Uses kubectl exec for streaming (shell-bridge doesn't support stdin streaming yet)
func (c *Client) RestoreWorkspace(ctx context.Context, namespace, podName string, tarData io.Reader) error {
	return c.Exec(ctx, namespace, podName, c.defaultContainer, []string{
		"tar", "xzf", "-", "-C", "/workspace",
	}, StreamOptions{
		Stdin: tarData,
	})
}
