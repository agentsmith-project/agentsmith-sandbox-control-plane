package k8s

import (
	"context"
	"fmt"
	"io"
	"log"
	"net/url"
	"sync"
	"time"

	"github.com/agentsmith-project/agentsmith-sandbox-control-plane/internal/observability"
	v1 "k8s.io/api/core/v1"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/kubernetes/scheme"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/remotecommand"
	utilexec "k8s.io/client-go/util/exec"
)

// ExecOptions contains options for executing commands in a pod
type ExecOptions struct {
	Command   []string
	Stdin     io.Reader
	Stdout    io.Writer
	Stderr    io.Writer
	TTY       bool
	Timeout   time.Duration
	Container string
}

// ExecResult contains the result of a command execution
type ExecResult struct {
	ExitCode int
	Stdout   string
	Stderr   string
	Duration time.Duration
	TimedOut bool
}

// Executor handles command execution in pods
type Executor struct {
	config     *rest.Config
	clientset  *kubernetes.Clientset
	restClient rest.Interface
	namespace  string
}

// NewExecutor creates a new executor
func NewExecutor(client *Client) *Executor {
	return &Executor{
		config:     client.config,
		clientset:  client.clientset,
		restClient: client.clientset.CoreV1().RESTClient(),
		namespace:  client.namespace,
	}
}

// Exec executes a command in a pod
func (e *Executor) Exec(ctx context.Context, podName string, opts *ExecOptions) (*ExecResult, error) {
	if opts == nil {
		opts = &ExecOptions{}
	}

	if opts.Container == "" {
		opts.Container = "main"
	}

	startTime := time.Now()

	// Apply timeout if specified
	execCtx := ctx
	if opts.Timeout > 0 {
		var cancel context.CancelFunc
		execCtx, cancel = context.WithTimeout(ctx, opts.Timeout)
		defer cancel()
	}

	// Capture output if not provided
	var stdoutBuf, stderrBuf *bufferWriter
	if opts.Stdout == nil {
		stdoutBuf = newBufferWriter()
		opts.Stdout = stdoutBuf
	}
	if opts.Stderr == nil {
		stderrBuf = newBufferWriter()
		opts.Stderr = stderrBuf
	}

	streamStdin := opts.Stdin != nil
	streamStdout := opts.Stdout != nil
	streamStderr := opts.Stderr != nil

	// Build exec URL (must include stdin=true when streaming input)
	execURL, err := e.buildExecURL(podName, opts.Command, opts.Container, opts.TTY, streamStdin, streamStdout, streamStderr)
	if err != nil {
		return nil, fmt.Errorf("failed to build exec URL: %w", err)
	}

	// Create SPDY executor
	exec, err := remotecommand.NewSPDYExecutor(e.config, "POST", execURL)
	if err != nil {
		return nil, fmt.Errorf("failed to create executor: %w", err)
	}

	// Execute the command
	streamOpts := remotecommand.StreamOptions{
		Stdin:  opts.Stdin,
		Stdout: opts.Stdout,
		Stderr: opts.Stderr,
		Tty:    opts.TTY,
	}

	err = exec.StreamWithContext(execCtx, streamOpts)

	duration := time.Since(startTime)

	// Capture output
	result := &ExecResult{
		Duration: duration,
	}

	// Try to get output from buffers
	if stdoutBuf != nil {
		result.Stdout = stdoutBuf.String()
	} else if opts.Stdout != nil {
		// If a writer was provided, try to get its output if it supports String()
		if str, ok := opts.Stdout.(interface{ String() string }); ok {
			result.Stdout = str.String()
		}
	}
	if stderrBuf != nil {
		result.Stderr = stderrBuf.String()
	} else if opts.Stderr != nil {
		// If a writer was provided, try to get its output if it supports String()
		if str, ok := opts.Stderr.(interface{ String() string }); ok {
			result.Stderr = str.String()
		}
	}

	// Check for timeout
	if execCtx.Err() == context.DeadlineExceeded {
		result.TimedOut = true
		log.Printf("Exec timeout after %v (expected %v)", duration, opts.Timeout)
		return result, fmt.Errorf("command timed out after %v", opts.Timeout)
	}

	if err != nil {
		// A non-zero process exit is reported via ExitError – extract the status
		// code and return it as a successful exec (process ran; it just exited non-zero).
		if exitErr, ok := err.(utilexec.ExitError); ok {
			result.ExitCode = exitErr.ExitStatus()
			log.Printf("Exec finished with non-zero exit code %d after %v", result.ExitCode, duration)
			return result, nil
		}
		result.ExitCode = -1
		log.Printf("Exec error after %v: %s", duration, observability.RedactLogValue(err))
		return result, err
	}

	result.ExitCode = 0
	return result, nil
}

// buildExecURL builds the exec URL for a pod
func (e *Executor) buildExecURL(podName string, command []string, container string, tty bool, stdin bool, stdout bool, stderr bool) (*url.URL, error) {
	req := e.restClient.Post().
		Resource("pods").
		Namespace(e.namespace).
		Name(podName).
		SubResource("exec")

	execOpts := &v1.PodExecOptions{
		Command:   command,
		Stdin:     stdin,
		Stdout:    stdout,
		Stderr:    stderr,
		TTY:       tty,
		Container: container,
	}

	// Use the v1 parameter codec for PodExecOptions
	req = req.VersionedParams(execOpts, scheme.ParameterCodec)

	return req.URL(), nil
}

// bufferWriter is a thread-safe buffer for capturing output
type bufferWriter struct {
	data []byte
	mu   sync.Mutex
}

func newBufferWriter() *bufferWriter {
	return &bufferWriter{data: make([]byte, 0, 4096)}
}

func (b *bufferWriter) Write(p []byte) (int, error) {
	b.mu.Lock()
	b.data = append(b.data, p...)
	b.mu.Unlock()
	return len(p), nil
}

func (b *bufferWriter) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return string(b.data)
}
