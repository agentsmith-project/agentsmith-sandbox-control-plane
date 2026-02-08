package k8s

import (
	"context"
	"fmt"
	"io"

	mboscontext "github.com/sandbox/manager/internal/context"
)

// SnapshotWorkspace creates a tar.gz of /workspace
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

		// Set a timeout for the snapshot operation using the dedicated snapshot timeout
		// This ensures the goroutine respects both parent cancellation and timeouts
		execCtx, cancel := mboscontext.WithSnapshotTimeout(ctx)
		defer cancel()

		// Execute tar command
		err := c.Exec(execCtx, namespace, podName, "runner", []string{
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
func (c *Client) RestoreWorkspace(ctx context.Context, namespace, podName string, tarData io.Reader) error {
	return c.Exec(ctx, namespace, podName, "runner", []string{
		"tar", "xzf", "-", "-C", "/workspace",
	}, StreamOptions{
		Stdin: tarData,
	})
}

// CheckTmux checks if tmux session exists
func (c *Client) CheckTmux(ctx context.Context, namespace, podName string) (bool, error) {
	output, err := c.ExecWithOutput(ctx, namespace, podName, "runner", []string{
		"tmux", "has-session", "-t", "sandbox",
	})

	if err != nil {
		return false, err
	}

	// Exit code 0 = session exists, 1 = not found
	return len(output) == 0, nil
}
