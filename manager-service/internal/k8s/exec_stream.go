package k8s

import (
	"bytes"
	"context"
	"fmt"
	"io"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/client-go/kubernetes/scheme"
	"k8s.io/client-go/tools/remotecommand"
)

// StreamOptions contains options for streaming exec I/O
type StreamOptions struct {
	Stdin  io.Reader
	Stdout io.Writer
	Stderr io.Writer
	TTY    bool
}

// Exec executes a command in a pod with streaming I/O support
func (c *Client) Exec(ctx context.Context, namespace, podName, container string, command []string, opts StreamOptions) error {
	req := c.clientset.CoreV1().RESTClient().Post().
		Resource("pods").
		Namespace(namespace).
		Name(podName).
		SubResource("exec").
		VersionedParams(&corev1.PodExecOptions{
			Container: container,
			Command:   command,
			Stdin:     opts.Stdin != nil,
			Stdout:    opts.Stdout != nil,
			Stderr:    opts.Stderr != nil,
			TTY:       opts.TTY,
		}, scheme.ParameterCodec)

	executor, err := remotecommand.NewSPDYExecutor(c.config, "POST", req.URL())
	if err != nil {
		return fmt.Errorf("failed to create executor: %w", err)
	}

	err = executor.StreamWithContext(ctx, remotecommand.StreamOptions{
		Stdin:  opts.Stdin,
		Stdout: opts.Stdout,
		Stderr: opts.Stderr,
		Tty:    opts.TTY,
	})
	if err != nil {
		return fmt.Errorf("exec stream failed: %w", err)
	}

	return nil
}

// ExecWithOutput runs a command and returns stdout as bytes
func (c *Client) ExecWithOutput(ctx context.Context, namespace, podName, container string, command []string) ([]byte, error) {
	var stdout bytes.Buffer
	err := c.Exec(ctx, namespace, podName, container, command, StreamOptions{
		Stdout: &stdout,
	})
	return stdout.Bytes(), err
}
