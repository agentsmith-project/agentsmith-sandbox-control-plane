package files

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"errors"
	"fmt"
	"io"
	"path/filepath"
	"strings"
	"time"

	"github.com/sandbox/manager/internal/k8s"
)

// ExecOptions defines the interface for executing commands in pods
// This abstracts the execution mechanism (kubectl exec vs shell-bridge)
type ExecOptions interface{}

// K8sExecutor defines the interface for executing commands in pods
type K8sExecutor interface {
	Exec(ctx context.Context, podName string, opts *k8s.ExecOptions) (*k8s.ExecResult, error)
}

// UploadConfig contains configuration for file uploads
type UploadConfig struct {
	RootPrefix     string
	DefaultDest    string
	MaxBytes       int64
	TarBin         string
	RejectSymlinks bool
}

// DownloadConfig contains configuration for file downloads
type DownloadConfig struct {
	RootPrefix string
	DefaultSrc string
	TarBin     string
}

// Uploader handles file uploads to sandbox pods
type Uploader struct {
	k8sExec K8sExecutor
	config  *UploadConfig
}

// UploadValidationError indicates the uploaded archive is invalid and should be rejected.
type UploadValidationError struct {
	cause error
}

func (e *UploadValidationError) Error() string {
	if e == nil || e.cause == nil {
		return "invalid archive"
	}
	return e.cause.Error()
}

func (e *UploadValidationError) Unwrap() error { return e.cause }

func IsUploadValidationError(err error) bool {
	var vErr *UploadValidationError
	return errors.As(err, &vErr)
}

// NewUploader creates a new file uploader
func NewUploader(k8sExec K8sExecutor, config *UploadConfig) *Uploader {
	return &Uploader{
		k8sExec: k8sExec,
		config:  config,
	}
}

func (u *Uploader) Upload(ctx context.Context, podName string, dest string, tarData io.Reader) error {
	// Validate and normalize destination
	dest, err := u.ValidateDest(dest)
	if err != nil {
		return fmt.Errorf("invalid destination: %w", err)
	}

	if u.config.MaxBytes > 0 {
		tarData = io.LimitReader(tarData, u.config.MaxBytes)
	}

	// Two-pass approach:
	// 1. Scan the archive for security validation
	// 2. Extract the validated archive

	// Read all data into memory for two-pass processing
	// In production, this could be optimized with a temp file
	var buf bytes.Buffer
	if _, err := io.Copy(&buf, tarData); err != nil {
		return fmt.Errorf("upload failed to read data: %w", err)
	}

	// Pass 1: Security validation
	if err := u.validateTarArchive(buf.Bytes(), dest); err != nil {
		return &UploadValidationError{cause: fmt.Errorf("upload validation failed: %w", err)}
	}

	// Ensure destination exists
	mkdirOpts := &k8s.ExecOptions{
		Command: []string{"mkdir", "-p", dest},
		Stdout:  new(bytes.Buffer),
		Stderr:  new(bytes.Buffer),
		TTY:     false,
		Timeout: 30 * time.Second,
	}
	if _, err := u.k8sExec.Exec(ctx, podName, mkdirOpts); err != nil {
		return fmt.Errorf("upload mkdir failed: %w", err)
	}

	// Pass 2: Extract validated archive
	cmd := u.buildUploadCommand(dest)

	opts := &k8s.ExecOptions{
		Command: cmd,
		Stdin:   &buf,
		Stdout:  new(bytes.Buffer),
		Stderr:  new(bytes.Buffer),
		TTY:     false,
		Timeout: 5 * time.Minute,
	}

	result, err := u.k8sExec.Exec(ctx, podName, opts)
	if err != nil {
		return fmt.Errorf("upload exec failed: %w", err)
	}

	// Check for errors in stderr
	if result.Stderr != "" {
		stderrMsg := result.Stderr
		if u.hasTarError(stderrMsg) {
			return fmt.Errorf("upload failed: %s", stderrMsg)
		}
	}

	return nil
}

// validateTarArchive validates the tar archive for security issues
// This implements a two-pass security check:
// - Allows: relative symlinks within sandbox, regular files and directories
// - Rejects: absolute symlinks, hardlinks, absolute paths, path traversal, special files
func (u *Uploader) validateTarArchive(data []byte, dest string) error {
	// First, try to ungzip the data
	var r io.Reader = bytes.NewReader(data)
	if IsGzipped(data) {
		gz, err := gzip.NewReader(bytes.NewReader(data))
		if err != nil {
			return fmt.Errorf("invalid gzip data: %w", err)
		}
		defer gz.Close()
		r = gz
	}

	tr := tar.NewReader(r)

	for {
		header, err := tr.Next()
		if err == io.EOF {
			break // End of archive
		}
		if err != nil {
			return fmt.Errorf("invalid tar archive: %w", err)
		}

		// Validate the entry
		if err := u.validateTarEntry(header, dest); err != nil {
			return fmt.Errorf("invalid entry %s: %w", header.Name, err)
		}
	}

	return nil
}

// validateTarEntry validates a single tar entry for security
func (u *Uploader) validateTarEntry(header *tar.Header, dest string) error {
	// Check for hardlinks (TypeLink)
	if header.Typeflag == tar.TypeLink {
		return fmt.Errorf("hardlinks are not allowed")
	}

	// Check for special files (device files, named pipes, etc.)
	switch header.Typeflag {
	case tar.TypeChar, tar.TypeBlock, tar.TypeFifo:
		return fmt.Errorf("special files (devices, fifos) are not allowed")
	}

	// Get the full path of the file
	fullPath := filepath.Join(dest, header.Name)

	// Clean the path to resolve any ".." components
	cleanPath := filepath.Clean(fullPath)

	// Ensure the path doesn't escape the destination directory
	rel, err := filepath.Rel(dest, cleanPath)
	if err != nil {
		return fmt.Errorf("invalid path: %w", err)
	}
	if strings.HasPrefix(rel, "..") || strings.HasPrefix(rel, "../") {
		return fmt.Errorf("path traversal detected: %s", header.Name)
	}

	// For symlinks, validate the link target
	if header.Typeflag == tar.TypeSymlink {
		// Allow relative symlinks that stay within sandbox
		// Reject absolute symlinks
		if filepath.IsAbs(header.Linkname) {
			return fmt.Errorf("absolute symlinks are not allowed: %s -> %s", header.Name, header.Linkname)
		}

		// Check if the relative symlink would escape the destination
		linkTarget := filepath.Clean(filepath.Join(filepath.Dir(fullPath), header.Linkname))
		linkRel, err := filepath.Rel(dest, linkTarget)
		if err != nil {
			return fmt.Errorf("invalid symlink target: %w", err)
		}
		if strings.HasPrefix(linkRel, "..") || strings.HasPrefix(linkRel, "../") {
			return fmt.Errorf("symlink escapes destination: %s -> %s", header.Name, header.Linkname)
		}

		// Relative symlinks within sandbox are allowed
		return nil
	}

	return nil
}

// ValidateDest validates and normalizes the destination path
func (u *Uploader) ValidateDest(dest string) (string, error) {
	if dest == "" {
		dest = u.config.DefaultDest
	}

	// Must be absolute path
	if !filepath.IsAbs(dest) {
		return "", fmt.Errorf("destination must be an absolute path: %s", dest)
	}

	// Must be under root prefix
	if !u.isUnderRoot(dest) {
		return "", fmt.Errorf("destination must be under root prefix %s: %s", u.config.RootPrefix, dest)
	}

	// Clean the path
	return filepath.Clean(dest), nil
}

// isUnderRoot checks if a path is under the root prefix
func (u *Uploader) isUnderRoot(path string) bool {
	rel, err := filepath.Rel(u.config.RootPrefix, path)
	if err != nil {
		return false
	}
	return !strings.HasPrefix(rel, "..")
}

// buildUploadCommand builds the command for extracting tar.gz
func (u *Uploader) buildUploadCommand(dest string) []string {
	// Use tar -xzf - -C <dest> to extract from stdin
	// Add --warning=none to suppress warnings
	// Add --no-same-owner to avoid permission issues
	cmd := []string{u.config.TarBin, "-xzf", "-", "-C", dest, "--warning=none", "--no-same-owner"}

	// Optionally reject symlinks for security
	if u.config.RejectSymlinks {
		// Note: GNU tar doesn't have a direct --no-symlinks option
		// We rely on the fact that symlinks in the archive are preserved
		// Additional validation would require a two-pass approach
	}

	return cmd
}

// hasTarError checks if stderr contains a tar error
func (u *Uploader) hasTarError(stderr string) bool {
	lower := strings.ToLower(stderr)
	// Look for actual errors, not warnings
	errorIndicators := []string{
		"error",
		"cannot open",
		"not found",
		"permission denied",
		"disk full",
		"no space left",
	}
	for _, indicator := range errorIndicators {
		if strings.Contains(lower, indicator) {
			return true
		}
	}
	return false
}

// Downloader handles file downloads from sandbox pods
type Downloader struct {
	k8sExec K8sExecutor
	config  *DownloadConfig
}

// NewDownloader creates a new file downloader
func NewDownloader(k8sExec K8sExecutor, config *DownloadConfig) *Downloader {
	return &Downloader{
		k8sExec: k8sExec,
		config:  config,
	}
}

// Download downloads files from a pod as a tar.gz stream
func (d *Downloader) Download(ctx context.Context, podName string, src string) (io.ReadCloser, error) {
	// Validate and normalize source
	src, err := d.ValidateSrc(src)
	if err != nil {
		return nil, fmt.Errorf("invalid source: %w", err)
	}

	// Build the tar creation command
	cmd := d.buildDownloadCommand(src)

	// Create a pipe for the tar output
	pr, pw := io.Pipe()

	// Execute the command in a goroutine
	go func() {
		defer pw.Close()

		// Check if context is already cancelled before starting
		select {
		case <-ctx.Done():
			pw.CloseWithError(ctx.Err())
			return
		default:
		}

		// Create a separate context with timeout for the exec operation
		// This ensures the exec is cancelled if the parent context is cancelled
		execCtx, cancel := context.WithTimeout(ctx, 120*time.Second)
		defer cancel()

		opts := &k8s.ExecOptions{
			Command: cmd,
			Stdout:  pw,
			Stderr:  new(bytes.Buffer),
			TTY:     false,
			Timeout: 120 * time.Second, // Download timeout
		}

		// Execute and handle context cancellation
		err := d.execWithContextCheck(execCtx, podName, opts, pw)
		if err != nil {
			pw.CloseWithError(fmt.Errorf("download exec failed: %w", err))
		}
	}()

	return pr, nil
}

// execWithContextCheck executes the command with proper context cancellation handling
func (d *Downloader) execWithContextCheck(ctx context.Context, podName string, opts *k8s.ExecOptions, pw *io.PipeWriter) error {
	// Create a channel to receive the exec result
	resultCh := make(chan error, 1)

	// Run the exec in a separate goroutine
	go func() {
		_, err := d.k8sExec.Exec(ctx, podName, opts)
		resultCh <- err
	}()

	// Wait for either completion or context cancellation
	select {
	case <-ctx.Done():
		// Context was cancelled, return the cancellation error
		return ctx.Err()
	case err := <-resultCh:
		// Exec completed (successfully or with an error)
		return err
	}
}

// ValidateSrc validates and normalizes the source path
func (d *Downloader) ValidateSrc(src string) (string, error) {
	if src == "" {
		src = d.config.DefaultSrc
	}

	// Must be absolute path
	if !filepath.IsAbs(src) {
		return "", fmt.Errorf("source must be an absolute path: %s", src)
	}

	// Must be under root prefix
	if !d.isUnderRoot(src) {
		return "", fmt.Errorf("source must be under root prefix %s: %s", d.config.RootPrefix, src)
	}

	// Clean the path
	return filepath.Clean(src), nil
}

// isUnderRoot checks if a path is under the root prefix
func (d *Downloader) isUnderRoot(path string) bool {
	rel, err := filepath.Rel(d.config.RootPrefix, path)
	if err != nil {
		return false
	}
	return !strings.HasPrefix(rel, "..")
}

// buildDownloadCommand builds the command for creating tar.gz
func (d *Downloader) buildDownloadCommand(src string) []string {
	// Use tar -czf - -C <src> . to create a gzipped tar archive to stdout
	// Add --warning=none to suppress warnings
	return []string{d.config.TarBin, "-czf", "-", "-C", src, ".", "--warning=none"}
}

// ValidatePath validates that a path is safe to use
func ValidatePath(path, rootPrefix string) error {
	// Must be absolute
	if !filepath.IsAbs(path) {
		return fmt.Errorf("path must be absolute: %s", path)
	}

	// Clean the path
	cleanPath := filepath.Clean(path)

	// Check if it's under root prefix
	rel, err := filepath.Rel(rootPrefix, cleanPath)
	if err != nil {
		return fmt.Errorf("invalid path: %w", err)
	}

	if strings.HasPrefix(rel, "..") {
		return fmt.Errorf("path escapes root prefix: %s", path)
	}

	return nil
}

// WrapGzipWriter wraps a writer with gzip compression
func WrapGzipWriter(w io.Writer) io.WriteCloser {
	return gzip.NewWriter(w)
}

// WrapGzipReader wraps a reader with gzip decompression
func WrapGzipReader(r io.Reader) (io.ReadCloser, error) {
	return gzip.NewReader(r)
}

// IsGzipped checks if data is gzipped by reading the magic number
func IsGzipped(data []byte) bool {
	if len(data) < 2 {
		return false
	}
	// Gzip magic number is 0x1f 0x8b
	return data[0] == 0x1f && data[1] == 0x8b
}

// FileList represents a list of files in a directory
type FileList struct {
	Files []FileInfo `json:"files"`
}

// FileInfo represents information about a file
type FileInfo struct {
	Name     string `json:"name"`
	Path     string `json:"path"`
	Size     int64  `json:"size"`
	Mode     string `json:"mode"`
	IsDir    bool   `json:"isDir"`
	Modified string `json:"modified,omitempty"`
}

// ListFiles lists files in a directory (for future use)
func ListFiles(ctx context.Context, k8sExec *k8s.Executor, podName, path string) (*FileList, error) {
	// Build find command
	cmd := []string{"find", path, "-maxdepth", "1", "-printf", "%p|%s|%f|%M\\n"}

	opts := &k8s.ExecOptions{
		Command: cmd,
		Stdout:  new(bytes.Buffer),
		Stderr:  new(bytes.Buffer),
		TTY:     false,
	}

	result, err := k8sExec.Exec(ctx, podName, opts)
	if err != nil {
		return nil, fmt.Errorf("list files failed: %w", err)
	}

	// Parse output
	output := result.Stdout
	if output == "" {
		return &FileList{Files: []FileInfo{}}, nil
	}

	// Parse each line
	lines := strings.Split(output, "\n")
	files := make([]FileInfo, 0, len(lines))

	for _, line := range lines {
		if line == "" {
			continue
		}

		parts := strings.Split(line, "|")
		if len(parts) < 4 {
			continue
		}

		files = append(files, FileInfo{
			Path: parts[0],
			// Size: parse size from parts[1]
			Name: parts[2],
			Mode: parts[3],
		})
	}

	return &FileList{Files: files}, nil
}
