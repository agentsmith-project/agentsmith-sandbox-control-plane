package k8s

import (
	"context"
	"fmt"
	"io"
)

// SnapshotWorkspace creates a tar.gz of /workspace
func (c *Client) SnapshotWorkspace(ctx context.Context, namespace, podName string) (io.ReadCloser, error) {
	reader, writer := io.Pipe()

	go func() {
		defer writer.Close()

		err := c.Exec(ctx, namespace, podName, "runner", []string{
			"tar", "czf", "-", "-C", "/workspace", ".",
		}, StreamOptions{
			Stdout: writer,
		})
		if err != nil {
			writer.CloseWithError(fmt.Errorf("tar command failed: %w", err))
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
