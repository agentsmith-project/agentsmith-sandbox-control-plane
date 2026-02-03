package k8s

import (
	"context"
	"fmt"
	"io"
	"log"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"

	v1 "k8s.io/api/core/v1"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/kubernetes/scheme"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/remotecommand"
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
		opts.Container = "runner" // default container name
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

	if stdoutBuf != nil {
		result.Stdout = stdoutBuf.String()
	}
	if stderrBuf != nil {
		result.Stderr = stderrBuf.String()
	}

	// Check for timeout
	if execCtx.Err() == context.DeadlineExceeded {
		result.TimedOut = true
		log.Printf("Exec timeout after %v (expected %v)", duration, opts.Timeout)
		return result, fmt.Errorf("command timed out after %v", opts.Timeout)
	}

	if err != nil {
		result.ExitCode = -1
		log.Printf("Exec error after %v: %v", duration, err)
		return result, err
	}

	result.ExitCode = 0
	return result, nil
}

// ExecWithExitCode executes a command and tries to extract the exit code
// from a marker in stderr (if configured)
func (e *Executor) ExecWithExitCode(ctx context.Context, podName string, opts *ExecOptions, markerKey string) (*ExecResult, error) {
	result, err := e.Exec(ctx, podName, opts)
	if err != nil {
		return result, err
	}

	// Try to extract exit code from marker
	if markerKey != "" {
		result.ExitCode = ExtractExitCodeMarker(result.Stderr, markerKey)
		// Remove the marker from stderr
		result.Stderr = RemoveExitCodeMarker(result.Stderr, markerKey)
	}

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

// ExtractExitCodeMarker extracts the exit code from a marker in stderr
// The marker format is: __SBX_EXIT_CODE__=<n>
func ExtractExitCodeMarker(stderr, markerKey string) int {
	// Find the marker in stderr
	markerPrefix := markerKey + "="
	idx := findLastMarkerStart(stderr, markerPrefix)
	if idx == -1 {
		return -1 // marker not found
	}

	// Extract the number after the marker
	exitCodeStr := stderr[idx+len(markerPrefix):]

	// The exit code should be at the end of a line
	// Find the end of the line (or end of string)
	lineEnd := strings.IndexAny(exitCodeStr, "\n\r")
	if lineEnd >= 0 {
		exitCodeStr = exitCodeStr[:lineEnd]
	}

	// Parse the exit code
	exitCode, err := strconv.Atoi(strings.TrimSpace(exitCodeStr))
	if err != nil {
		return -1
	}

	return exitCode
}

// RemoveExitCodeMarker removes the exit code marker from output
func RemoveExitCodeMarker(output, markerKey string) string {
	markerPrefix := markerKey + "="
	idx := findLastMarkerStart(output, markerPrefix)
	if idx < 0 {
		return output
	}

	// Find the start of the line containing the marker
	lineStart := idx
	for lineStart > 0 && output[lineStart-1] != '\n' && output[lineStart-1] != '\r' {
		lineStart--
	}

	// Find the end of the marker line
	lineEnd := idx
	for lineEnd < len(output) && output[lineEnd] != '\n' && output[lineEnd] != '\r' {
		lineEnd++
	}

	// Skip past the line ending
	if lineEnd < len(output) && (output[lineEnd] == '\n' || output[lineEnd] == '\r') {
		lineEnd++
		if lineEnd < len(output) && output[lineEnd-1] == '\r' && output[lineEnd] == '\n' {
			// Handle Windows CRLF
			lineEnd++
		}
	}

	// Remove the marker line
	return output[:lineStart] + output[lineEnd:]
}

// findLastMarkerStart finds the start of the last marker in output
func findLastMarkerStart(output, markerPrefix string) int {
	lastIdx := -1
	searchFrom := 0

	for {
		idx := strings.Index(output[searchFrom:], markerPrefix)
		if idx < 0 {
			break
		}

		// Adjust to absolute position
		absIdx := searchFrom + idx

		// Verify this looks like a marker (should be at line start or after whitespace)
		if absIdx == 0 || output[absIdx-1] == ' ' || output[absIdx-1] == '\t' ||
			output[absIdx-1] == '\n' || output[absIdx-1] == '\r' {
			lastIdx = absIdx
		}

		searchFrom = absIdx + len(markerPrefix)
	}

	return lastIdx
}

// indexOf finds the first occurrence of substr in s starting from idx
func indexOf(s, substr string, idx int) int {
	if idx < 0 {
		idx = 0
	}
	if idx >= len(s) {
		return -1
	}
	if substr == "" {
		return idx
	}

	for i := idx; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return i
		}
	}
	return -1
}
