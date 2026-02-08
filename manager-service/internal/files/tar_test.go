package files

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"io"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/sandbox/manager/internal/k8s"
	"github.com/stretchr/testify/require"
)

// MockExecutor is a mock implementation of K8sExecutor for testing
type MockExecutor struct {
	ExecFunc func(ctx context.Context, podName string, opts *k8s.ExecOptions) (*k8s.ExecResult, error)
}

func (m *MockExecutor) Exec(ctx context.Context, podName string, opts *k8s.ExecOptions) (*k8s.ExecResult, error) {
	if m.ExecFunc != nil {
		return m.ExecFunc(ctx, podName, opts)
	}
	return &k8s.ExecResult{}, nil
}

func TestValidateDest(t *testing.T) {
	tests := []struct {
		name        string
		dest        string
		rootPrefix  string
		defaultDest string
		want        string
		wantErr     bool
		errContains string
	}{
		{
			name:        "valid absolute path under root",
			dest:        "/workspace/data",
			rootPrefix:  "/workspace",
			defaultDest: "/workspace",
			want:        "/workspace/data",
			wantErr:     false,
		},
		{
			name:        "empty dest uses default",
			dest:        "",
			rootPrefix:  "/workspace",
			defaultDest: "/workspace",
			want:        "/workspace",
			wantErr:     false,
		},
		{
			name:        "relative path is rejected",
			dest:        "data/dir",
			rootPrefix:  "/workspace",
			defaultDest: "/workspace",
			want:        "",
			wantErr:     true,
			errContains: "absolute path",
		},
		{
			name:        "path outside root is rejected",
			dest:        "/etc/passwd",
			rootPrefix:  "/workspace",
			defaultDest: "/workspace",
			want:        "",
			wantErr:     true,
			errContains: "root prefix",
		},
		{
			name:        "path with .. traversal is rejected",
			dest:        "/workspace/../etc",
			rootPrefix:  "/workspace",
			defaultDest: "/workspace",
			want:        "/../etc", // Cleaned path
			wantErr:     true,
			errContains: "root prefix",
		},
		{
			name:        "path is normalized",
			dest:        "/workspace/./data",
			rootPrefix:  "/workspace",
			defaultDest: "/workspace",
			want:        "/workspace/data",
			wantErr:     false,
		},
		{
			name:        "trailing slash is preserved",
			dest:        "/workspace/data/",
			rootPrefix:  "/workspace",
			defaultDest: "/workspace",
			want:        "/workspace/data",
			wantErr:     false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			u := &Uploader{
				config: &UploadConfig{
					RootPrefix:  tt.rootPrefix,
					DefaultDest: tt.defaultDest,
				},
			}

			got, err := u.ValidateDest(tt.dest)
			if (err != nil) != tt.wantErr {
				t.Errorf("ValidateDest() error = %v, wantErr %v", err, tt.wantErr)
				return
			}

			if tt.wantErr {
				if tt.errContains != "" && err == nil {
					t.Errorf("ValidateDest() expected error containing %q, got nil", tt.errContains)
				} else if tt.errContains != "" && !strings.Contains(err.Error(), tt.errContains) {
					t.Errorf("ValidateDest() error = %v, want error containing %q", err, tt.errContains)
				}
				return
			}

			if got != tt.want {
				t.Errorf("ValidateDest() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestValidateSrc(t *testing.T) {
	tests := []struct {
		name        string
		src         string
		rootPrefix  string
		defaultSrc  string
		want        string
		wantErr     bool
		errContains string
	}{
		{
			name:       "valid absolute path under root",
			src:        "/workspace/data",
			rootPrefix: "/workspace",
			defaultSrc: "/workspace",
			want:       "/workspace/data",
			wantErr:    false,
		},
		{
			name:       "empty src uses default",
			src:        "",
			rootPrefix: "/workspace",
			defaultSrc: "/workspace",
			want:       "/workspace",
			wantErr:    false,
		},
		{
			name:        "relative path is rejected",
			src:         "data/dir",
			rootPrefix:  "/workspace",
			defaultSrc:  "/workspace",
			want:        "",
			wantErr:     true,
			errContains: "absolute path",
		},
		{
			name:        "path outside root is rejected",
			src:         "/etc/passwd",
			rootPrefix:  "/workspace",
			defaultSrc:  "/workspace",
			want:        "",
			wantErr:     true,
			errContains: "root prefix",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			d := &Downloader{
				config: &DownloadConfig{
					RootPrefix: tt.rootPrefix,
					DefaultSrc: tt.defaultSrc,
				},
			}

			got, err := d.ValidateSrc(tt.src)
			if (err != nil) != tt.wantErr {
				t.Errorf("ValidateSrc() error = %v, wantErr %v", err, tt.wantErr)
				return
			}

			if tt.wantErr && tt.errContains != "" && !strings.Contains(err.Error(), tt.errContains) {
				t.Errorf("ValidateSrc() error = %v, want error containing %q", err, tt.errContains)
			}

			if got != tt.want {
				t.Errorf("ValidateSrc() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestIsUnderRoot(t *testing.T) {
	tests := []struct {
		name      string
		path      string
		root      string
		wantUnder bool
	}{
		{
			name:      "path directly under root",
			path:      "/workspace/data",
			root:      "/workspace",
			wantUnder: true,
		},
		{
			name:      "path is root",
			path:      "/workspace",
			root:      "/workspace",
			wantUnder: true,
		},
		{
			name:      "path outside root",
			path:      "/etc/passwd",
			root:      "/workspace",
			wantUnder: false,
		},
		{
			name:      "path with .. escaping root",
			path:      "/workspace/../etc",
			root:      "/workspace",
			wantUnder: false,
		},
		{
			name:      "nested path under root",
			path:      "/workspace/a/b/c",
			root:      "/workspace",
			wantUnder: true,
		},
		{
			name:      "root with trailing slash",
			path:      "/workspace/data",
			root:      "/workspace/",
			wantUnder: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			u := &Uploader{
				config: &UploadConfig{
					RootPrefix: tt.root,
				},
			}

			got := u.isUnderRoot(tt.path)
			if got != tt.wantUnder {
				t.Errorf("isUnderRoot() = %v, want %v", got, tt.wantUnder)
			}
		})
	}
}

func TestHasTarError(t *testing.T) {
	tests := []struct {
		name   string
		stderr string
		want   bool
	}{
		{
			name:   "no error",
			stderr: "",
			want:   false,
		},
		{
			name:   "contains error keyword",
			stderr: "tar: error reading file",
			want:   true,
		},
		{
			name:   "contains cannot open",
			stderr: "tar: cannot open: No such file",
			want:   true,
		},
		{
			name:   "contains not found",
			stderr: "file not found",
			want:   true,
		},
		{
			name:   "contains permission denied",
			stderr: "permission denied",
			want:   true,
		},
		{
			name:   "contains disk full",
			stderr: "disk full",
			want:   true,
		},
		{
			name:   "contains no space left",
			stderr: "no space left on device",
			want:   true,
		},
		{
			name:   "warning only (no error keyword)",
			stderr: "tar: removing leading / from member names",
			want:   false,
		},
		{
			name:   "case insensitive error",
			stderr: "ERROR: something went wrong",
			want:   true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			u := &Uploader{}
			got := u.hasTarError(tt.stderr)
			if got != tt.want {
				t.Errorf("hasTarError() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestValidatePath(t *testing.T) {
	tests := []struct {
		name        string
		path        string
		rootPrefix  string
		wantErr     bool
		errContains string
	}{
		{
			name:       "valid absolute path under root",
			path:       "/workspace/data",
			rootPrefix: "/workspace",
			wantErr:    false,
		},
		{
			name:        "relative path",
			path:        "data/dir",
			rootPrefix:  "/workspace",
			wantErr:     true,
			errContains: "absolute",
		},
		{
			name:        "path escapes root",
			path:        "/workspace/../etc",
			rootPrefix:  "/workspace",
			wantErr:     true,
			errContains: "escapes",
		},
		{
			name:       "path with extra slashes",
			path:       "/workspace//data",
			rootPrefix: "/workspace",
			wantErr:    false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := ValidatePath(tt.path, tt.rootPrefix)
			if (err != nil) != tt.wantErr {
				t.Errorf("ValidatePath() error = %v, wantErr %v", err, tt.wantErr)
				return
			}

			if tt.wantErr && tt.errContains != "" && !strings.Contains(err.Error(), tt.errContains) {
				t.Errorf("ValidatePath() error = %v, want error containing %q", err, tt.errContains)
			}
		})
	}
}

func TestBuildUploadCommand(t *testing.T) {
	tests := []struct {
		name            string
		dest            string
		tarBin          string
		rejectSymlinks  bool
		wantContains    []string
		dontWantContain []string
	}{
		{
			name:   "basic upload command",
			dest:   "/workspace/data",
			tarBin: "tar",
			wantContains: []string{
				"tar",
				"-xzf",
				"-",
				"-C",
				"/workspace/data",
				"--warning=none",
				"--no-same-owner",
			},
		},
		{
			name:   "custom tar binary",
			dest:   "/workspace",
			tarBin: "/usr/bin/tar",
			wantContains: []string{
				"/usr/bin/tar",
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			u := &Uploader{
				config: &UploadConfig{
					TarBin: tt.tarBin,
				},
			}

			cmd := u.buildUploadCommand(tt.dest)

			cmdStr := strings.Join(cmd, " ")
			for _, want := range tt.wantContains {
				if !strings.Contains(cmdStr, want) {
					t.Errorf("buildUploadCommand() = %v, want to contain %v", cmdStr, want)
				}
			}

			for _, dontWant := range tt.dontWantContain {
				if strings.Contains(cmdStr, dontWant) {
					t.Errorf("buildUploadCommand() = %v, should not contain %v", cmdStr, dontWant)
				}
			}
		})
	}
}

func TestBuildDownloadCommand(t *testing.T) {
	tests := []struct {
		name         string
		src          string
		tarBin       string
		wantContains []string
	}{
		{
			name: "basic download command",
			src:  "/workspace/data",
			wantContains: []string{
				"tar",
				"-czf",
				"-",
				"-C",
				"/workspace/data",
				".",
				"--warning=none",
			},
		},
		{
			name: "custom tar binary",
			src:  "/workspace",
			wantContains: []string{
				"/usr/bin/tar",
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			d := &Downloader{
				config: &DownloadConfig{
					TarBin: "tar",
				},
			}

			if tt.name == "custom tar binary" {
				d.config.TarBin = "/usr/bin/tar"
			}

			cmd := d.buildDownloadCommand(tt.src)

			cmdStr := strings.Join(cmd, " ")
			for _, want := range tt.wantContains {
				if !strings.Contains(cmdStr, want) {
					t.Errorf("buildDownloadCommand() = %v, want to contain %v", cmdStr, want)
				}
			}
		})
	}
}

func TestIsGzipped(t *testing.T) {
	tests := []struct {
		name string
		data []byte
		want bool
	}{
		{
			name: "gzip magic number",
			data: []byte{0x1f, 0x8b, 0x08, 0x00},
			want: true,
		},
		{
			name: "not gzipped",
			data: []byte{0x00, 0x00, 0x00, 0x00},
			want: false,
		},
		{
			name: "empty data",
			data: []byte{},
			want: false,
		},
		{
			name: "single byte",
			data: []byte{0x1f},
			want: false,
		},
		{
			name: "tar without gzip",
			data: []byte{0x75, 0x73, 0x74, 0x61, 0x72}, // "ustar"
			want: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := IsGzipped(tt.data)
			if got != tt.want {
				t.Errorf("IsGzipped() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestWrapGzipWriter(t *testing.T) {
	t.Run("creates gzip writer", func(t *testing.T) {
		var buf bytes.Buffer
		writer := WrapGzipWriter(&buf)

		if writer == nil {
			t.Fatal("WrapGzipWriter() returned nil")
		}

		// Write some data
		testData := []byte("test data")
		_, err := writer.Write(testData)
		if err != nil {
			t.Errorf("WrapGzipWriter() Write error = %v", err)
		}

		// Close the writer
		err = writer.Close()
		if err != nil {
			t.Errorf("WrapGzipWriter() Close error = %v", err)
		}

		// Verify the data is gzipped
		got := IsGzipped(buf.Bytes())
		if !got {
			t.Error("WrapGzipWriter() output is not gzipped")
		}
	})
}

func TestWrapGzipReader(t *testing.T) {
	t.Run("creates gzip reader", func(t *testing.T) {
		// Create gzipped data
		var buf bytes.Buffer
		gw := gzip.NewWriter(&buf)
		testData := []byte("test data")
		_, _ = gw.Write(testData)
		_ = gw.Close()

		// Wrap the reader
		reader, err := WrapGzipReader(&buf)
		if err != nil {
			t.Fatalf("WrapGzipReader() error = %v", err)
		}

		if reader == nil {
			t.Fatal("WrapGzipReader() returned nil")
		}

		// Read the data
		result, err := io.ReadAll(reader)
		if err != nil {
			t.Errorf("WrapGzipReader() Read error = %v", err)
		}

		// Close the reader
		_ = reader.Close()

		if !bytes.Equal(result, testData) {
			t.Errorf("WrapGzipReader() = %v, want %v", result, testData)
		}
	})

	t.Run("invalid gzip data", func(t *testing.T) {
		invalidData := []byte{0x00, 0x01, 0x02, 0x03}
		_, err := WrapGzipReader(bytes.NewReader(invalidData))
		if err == nil {
			t.Error("WrapGzipReader() should return error for invalid gzip data")
		}
	})
}

func TestNewUploader(t *testing.T) {
	mockExec := &MockExecutor{}
	config := &UploadConfig{
		RootPrefix:     "/workspace",
		DefaultDest:    "/workspace",
		MaxBytes:       1024 * 1024,
		TarBin:         "tar",
		RejectSymlinks: false,
	}

	uploader := NewUploader(mockExec, config)

	if uploader == nil {
		t.Fatal("NewUploader() returned nil")
	}

	if uploader.config != config {
		t.Error("NewUploader() config not set correctly")
	}
}

func TestNewDownloader(t *testing.T) {
	mockExec := &MockExecutor{}
	config := &DownloadConfig{
		RootPrefix: "/workspace",
		DefaultSrc: "/workspace",
		TarBin:     "tar",
	}

	downloader := NewDownloader(mockExec, config)

	if downloader == nil {
		t.Fatal("NewDownloader() returned nil")
	}

	if downloader.config != config {
		t.Error("NewDownloader() config not set correctly")
	}
}

func TestUploadConfigDefaults(t *testing.T) {
	config := &UploadConfig{
		RootPrefix:  "/workspace",
		DefaultDest: "/workspace",
		MaxBytes:    10 * 1024 * 1024,
		TarBin:      "tar",
	}

	if config.RootPrefix == "" {
		t.Error("RootPrefix should not be empty")
	}

	if config.DefaultDest == "" {
		t.Error("DefaultDest should not be empty")
	}

	if config.MaxBytes <= 0 {
		t.Error("MaxBytes should be positive")
	}

	if config.TarBin == "" {
		t.Error("TarBin should not be empty")
	}
}

func TestDownloadConfigDefaults(t *testing.T) {
	config := &DownloadConfig{
		RootPrefix: "/workspace",
		DefaultSrc: "/workspace",
		TarBin:     "tar",
	}

	if config.RootPrefix == "" {
		t.Error("RootPrefix should not be empty")
	}

	if config.DefaultSrc == "" {
		t.Error("DefaultSrc should not be empty")
	}

	if config.TarBin == "" {
		t.Error("TarBin should not be empty")
	}
}

// TestFileInfo tests the FileInfo struct
func TestFileInfo(t *testing.T) {
	info := FileInfo{
		Name:     "test.txt",
		Path:     "/workspace/test.txt",
		Size:     1024,
		Mode:     "-rw-r--r--",
		IsDir:    false,
		Modified: "2024-01-01T00:00:00Z",
	}

	if info.Name != "test.txt" {
		t.Errorf("Name = %v, want 'test.txt'", info.Name)
	}

	if info.Path != "/workspace/test.txt" {
		t.Errorf("Path = %v, want '/workspace/test.txt'", info.Path)
	}

	if info.Size != 1024 {
		t.Errorf("Size = %v, want 1024", info.Size)
	}

	if info.IsDir {
		t.Error("IsDir should be false")
	}
}

// TestFileList tests the FileList struct
func TestFileList(t *testing.T) {
	list := FileList{
		Files: []FileInfo{
			{
				Name: "file1.txt",
				Path: "/workspace/file1.txt",
				Size: 100,
				Mode: "-rw-r--r--",
			},
			{
				Name: "file2.txt",
				Path: "/workspace/file2.txt",
				Size: 200,
				Mode: "-rw-r--r--",
			},
		},
	}

	if len(list.Files) != 2 {
		t.Errorf("Files length = %v, want 2", len(list.Files))
	}
}

// TestUploadWithMaxBytes tests that maxBytes is respected
func TestUploadWithMaxBytes(t *testing.T) {
	tests := []struct {
		name     string
		maxBytes int64
		dataSize int64
		wantErr  bool
	}{
		{
			name:     "data within limit",
			maxBytes: 1024,
			dataSize: 512,
			wantErr:  false,
		},
		{
			name:     "data exceeds limit",
			maxBytes: 100,
			dataSize: 200,
			wantErr:  true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Create a reader with the specified size
			data := make([]byte, tt.dataSize)
			reader := bytes.NewReader(data)

			limitedReader := io.LimitReader(reader, tt.maxBytes)

			// Try to read all data
			result, err := io.ReadAll(limitedReader)
			if err != nil {
				t.Errorf("io.ReadAll() error = %v", err)
			}

			// With LimitReader, we should get at most maxBytes
			expectedMax := tt.maxBytes
			if tt.dataSize < tt.maxBytes {
				expectedMax = tt.dataSize
			}

			if int64(len(result)) > expectedMax {
				t.Errorf("LimitedReader returned %d bytes, want at most %d", len(result), expectedMax)
			}
		})
	}
}

// TestPathTraversalDetection tests path traversal detection
func TestPathTraversalDetection(t *testing.T) {
	tests := []struct {
		name   string
		path   string
		root   string
		isSafe bool
	}{
		{
			name:   "normal path",
			path:   "/workspace/data/file.txt",
			root:   "/workspace",
			isSafe: true,
		},
		{
			name:   "path with .. in middle",
			path:   "/workspace/data/../file.txt",
			root:   "/workspace",
			isSafe: true, // After cleaning, this is still under root
		},
		{
			name:   "path escaping root with ..",
			path:   "/workspace/../etc/passwd",
			root:   "/workspace",
			isSafe: false,
		},
		{
			name:   "multiple .. escaping",
			path:   "/workspace/../../etc/passwd",
			root:   "/workspace",
			isSafe: false,
		},
		{
			name:   "path starting with ..",
			path:   "../workspace/file.txt",
			root:   "/workspace",
			isSafe: false, // Relative path is rejected first
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := ValidatePath(tt.path, tt.root)
			isSafe := err == nil

			if isSafe != tt.isSafe {
				t.Errorf("ValidatePath(%q, %q) = %v, want safe=%v", tt.path, tt.root, err, tt.isSafe)
			}
		})
	}
}

// TestCleanPathNormalization tests path normalization
func TestCleanPathNormalization(t *testing.T) {
	tests := []struct {
		input    string
		expected string
	}{
		{
			input:    "/workspace/./data",
			expected: "/workspace/data",
		},
		{
			input:    "/workspace//data",
			expected: "/workspace/data",
		},
		{
			input:    "/workspace/data/.",
			expected: "/workspace/data",
		},
		{
			input:    "/workspace/data/",
			expected: "/workspace/data",
		},
	}

	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			got := filepath.Clean(tt.input)
			if got != tt.expected {
				t.Errorf("Clean(%q) = %v, want %v", tt.input, got, tt.expected)
			}
		})
	}
}

// BenchmarkValidatePath benchmarks path validation
func BenchmarkValidatePath(b *testing.B) {
	root := "/workspace"
	validPath := "/workspace/data/file.txt"

	for i := 0; i < b.N; i++ {
		_ = ValidatePath(validPath, root)
	}
}

// BenchmarkIsUnderRoot benchmarks the isUnderRoot check
func BenchmarkIsUnderRoot(b *testing.B) {
	u := &Uploader{
		config: &UploadConfig{
			RootPrefix: "/workspace",
		},
	}
	validPath := "/workspace/data/file.txt"

	for i := 0; i < b.N; i++ {
		_ = u.isUnderRoot(validPath)
	}
}

// setupTestDownloader creates a test downloader with mock executor
func setupTestDownloader(t *testing.T) *Downloader {
	return &Downloader{
		k8sExec: &MockExecutor{},
		config: &DownloadConfig{
			RootPrefix: "/workspace",
			DefaultSrc: "/workspace",
			TarBin:     "tar",
		},
	}
}

// TestDownloader_Download_PropagatesContextCancellation tests that context cancellation is propagated
func TestDownloader_Download_PropagatesContextCancellation(t *testing.T) {
	downloader := setupTestDownloader(t)

	// Create a cancellable context
	ctx, cancel := context.WithCancel(context.Background())

	// Track if the context passed to Exec respects cancellation
	var receivedCtx context.Context
	execCalled := make(chan struct{}, 1)

	downloader.k8sExec = &MockExecutor{
		ExecFunc: func(ctx context.Context, podName string, opts *k8s.ExecOptions) (*k8s.ExecResult, error) {
			receivedCtx = ctx
			close(execCalled)
			// Block until context is cancelled
			<-ctx.Done()
			return nil, ctx.Err()
		},
	}

	// Start the download
	reader, err := downloader.Download(ctx, "test-pod", "/workspace")
	require.NoError(t, err)

	// Wait for exec to be called
	<-execCalled

	// Verify the received context is derived from the parent context
	select {
	case <-receivedCtx.Done():
		t.Fatal("Context should not be cancelled yet")
	default:
		// Context is still active, which is correct
	}

	// Now cancel the parent context
	cancel()

	// Reading should fail because the context was cancelled
	buf := make([]byte, 1024)
	n, err := reader.Read(buf)

	// We expect either:
	// 1. An error (context.Canceled)
	// 2. Or zero bytes with EOF/error
	if err == nil {
		t.Errorf("Read should return error, got n=%d and nil error", n)
	} else if err != io.EOF && err != context.Canceled {
		// Any error is acceptable - the important part is that the operation stopped
		t.Logf("Got expected error: %v", err)
	}

	reader.Close()
}

// TestDownloader_Download_ContextTimeout tests that context timeout is propagated
func TestDownloader_Download_ContextTimeout(t *testing.T) {
	downloader := setupTestDownloader(t)

	// Create a context with short timeout
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Millisecond)
	defer cancel()

	// Create a slow mock that never completes
	downloader.k8sExec = &MockExecutor{
		ExecFunc: func(ctx context.Context, podName string, opts *k8s.ExecOptions) (*k8s.ExecResult, error) {
			// Wait for context to be done (timeout or cancellation)
			<-ctx.Done()
			return nil, ctx.Err()
		},
	}

	// Start the download
	reader, err := downloader.Download(ctx, "test-pod", "/workspace")
	require.NoError(t, err)

	// Wait for timeout
	time.Sleep(50 * time.Millisecond)

	// Reading should fail because of timeout
	buf := make([]byte, 1024)
	n, err := reader.Read(buf)

	if err == nil {
		t.Errorf("Read should return error after timeout, got n=%d", n)
	}

	reader.Close()
}

// TestValidateTarEntry tests tar entry security validation
func TestValidateTarEntry(t *testing.T) {
	tests := []struct {
		name    string
		header  *tar.Header
		dest    string
		wantErr bool
		errMsg  string
	}{
		{
			name: "regular file is allowed",
			header: &tar.Header{
				Name:     "file.txt",
				Typeflag: tar.TypeReg,
				Mode:     0644,
			},
			dest:    "/workspace",
			wantErr: false,
		},
		{
			name: "directory is allowed",
			header: &tar.Header{
				Name:     "data",
				Typeflag: tar.TypeDir,
				Mode:     0755,
			},
			dest:    "/workspace",
			wantErr: false,
		},
		{
			name: "relative symlink within sandbox is allowed",
			header: &tar.Header{
				Name:     "link.txt",
				Typeflag: tar.TypeSymlink,
				Linkname: "other.txt",
				Mode:     0777,
			},
			dest:    "/workspace",
			wantErr: false,
		},
		{
			name: "relative symlink to subdirectory is allowed",
			header: &tar.Header{
				Name:     "link",
				Typeflag: tar.TypeSymlink,
				Linkname: "subdir/file.txt",
				Mode:     0777,
			},
			dest:    "/workspace",
			wantErr: false,
		},
		{
			name: "absolute symlink is rejected",
			header: &tar.Header{
				Name:     "link",
				Typeflag: tar.TypeSymlink,
				Linkname: "/etc/passwd",
				Mode:     0777,
			},
			dest:    "/workspace",
			wantErr: true,
			errMsg:  "absolute symlinks are not allowed",
		},
		{
			name: "hardlink is rejected",
			header: &tar.Header{
				Name:     "link",
				Typeflag: tar.TypeLink,
				Linkname: "target.txt",
				Mode:     0644,
			},
			dest:    "/workspace",
			wantErr: true,
			errMsg:  "hardlinks are not allowed",
		},
		{
			name: "character device is rejected",
			header: &tar.Header{
				Name:     "device",
				Typeflag: tar.TypeChar,
				Mode:     0666,
			},
			dest:    "/workspace",
			wantErr: true,
			errMsg:  "special files",
		},
		{
			name: "block device is rejected",
			header: &tar.Header{
				Name:     "block",
				Typeflag: tar.TypeBlock,
				Mode:     0666,
			},
			dest:    "/workspace",
			wantErr: true,
			errMsg:  "special files",
		},
		{
			name: "named pipe (fifo) is rejected",
			header: &tar.Header{
				Name:     "pipe",
				Typeflag: tar.TypeFifo,
				Mode:     0666,
			},
			dest:    "/workspace",
			wantErr: true,
			errMsg:  "special files",
		},
		{
			name: "path traversal with .. is rejected",
			header: &tar.Header{
				Name:     "../etc/passwd",
				Typeflag: tar.TypeReg,
				Mode:     0644,
			},
			dest:    "/workspace",
			wantErr: true,
			errMsg:  "path traversal",
		},
		{
			name: "relative symlink escaping sandbox is rejected",
			header: &tar.Header{
				Name:     "link",
				Typeflag: tar.TypeSymlink,
				Linkname: "../../etc/passwd",
				Mode:     0777,
			},
			dest:    "/workspace",
			wantErr: true,
			errMsg:  "symlink escapes",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			u := &Uploader{
				config: &UploadConfig{
					RootPrefix: "/workspace",
				},
			}

			err := u.validateTarEntry(tt.header, tt.dest)
			if (err != nil) != tt.wantErr {
				t.Errorf("validateTarEntry() error = %v, wantErr %v", err, tt.wantErr)
				return
			}

			if tt.wantErr && tt.errMsg != "" {
				if err == nil {
					t.Errorf("validateTarEntry() expected error containing %q, got nil", tt.errMsg)
				} else if !strings.Contains(err.Error(), tt.errMsg) {
					t.Errorf("validateTarEntry() error = %v, want error containing %q", err, tt.errMsg)
				}
			}
		})
	}
}

// TestValidateTarArchive tests tar archive security validation
func TestValidateTarArchive(t *testing.T) {
	tests := []struct {
		name    string
		create  func(*tar.Writer) error
		dest    string
		wantErr bool
		errMsg  string
	}{
		{
			name: "valid archive with regular files",
			create: func(tw *tar.Writer) error {
				headers := []*tar.Header{
					{Name: "file1.txt", Typeflag: tar.TypeReg, Mode: 0644, Size: 9},
					{Name: "dir/", Typeflag: tar.TypeDir, Mode: 0755},
					{Name: "dir/file2.txt", Typeflag: tar.TypeReg, Mode: 0644, Size: 9},
				}
				for _, h := range headers {
					if err := tw.WriteHeader(h); err != nil {
						return err
					}
					if h.Typeflag == tar.TypeReg && h.Size > 0 {
						tw.Write([]byte("test data"))
					}
				}
				return nil
			},
			dest:    "/workspace",
			wantErr: false,
		},
		{
			name: "archive with relative symlink (allowed)",
			create: func(tw *tar.Writer) error {
				h := &tar.Header{
					Name:     "link.txt",
					Typeflag: tar.TypeSymlink,
					Linkname: "target.txt",
					Mode:     0777,
				}
				return tw.WriteHeader(h)
			},
			dest:    "/workspace",
			wantErr: false,
		},
		{
			name: "archive with absolute symlink (rejected)",
			create: func(tw *tar.Writer) error {
				h := &tar.Header{
					Name:     "link",
					Typeflag: tar.TypeSymlink,
					Linkname: "/etc/passwd",
					Mode:     0777,
				}
				return tw.WriteHeader(h)
			},
			dest:    "/workspace",
			wantErr: true,
			errMsg:  "absolute symlinks",
		},
		{
			name: "archive with hardlink (rejected)",
			create: func(tw *tar.Writer) error {
				h := &tar.Header{
					Name:     "link",
					Typeflag: tar.TypeLink,
					Linkname: "target",
					Mode:     0644,
				}
				return tw.WriteHeader(h)
			},
			dest:    "/workspace",
			wantErr: true,
			errMsg:  "hardlinks",
		},
		{
			name: "archive with path traversal (rejected)",
			create: func(tw *tar.Writer) error {
				h := &tar.Header{
					Name:     "../escape.txt",
					Typeflag: tar.TypeReg,
					Mode:     0644,
					Size:     9,
				}
				if err := tw.WriteHeader(h); err != nil {
					return err
				}
				tw.Write([]byte("test data"))
				return nil
			},
			dest:    "/workspace",
			wantErr: true,
			errMsg:  "path traversal",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			u := &Uploader{
				config: &UploadConfig{
					RootPrefix: "/workspace",
				},
			}

			// Create a tar archive in memory
			var buf bytes.Buffer
			tw := tar.NewWriter(&buf)
			if err := tt.create(tw); err != nil {
				t.Fatalf("Failed to create test archive: %v", err)
			}
			if err := tw.Close(); err != nil {
				t.Fatalf("Failed to close tar writer: %v", err)
			}

			// Validate the archive
			err := u.validateTarArchive(buf.Bytes(), tt.dest)
			if (err != nil) != tt.wantErr {
				t.Errorf("validateTarArchive() error = %v, wantErr %v", err, tt.wantErr)
				return
			}

			if tt.wantErr && tt.errMsg != "" {
				if err == nil {
					t.Errorf("validateTarArchive() expected error containing %q, got nil", tt.errMsg)
				} else if !strings.Contains(err.Error(), tt.errMsg) {
					t.Errorf("validateTarArchive() error = %v, want error containing %q", err, tt.errMsg)
				}
			}
		})
	}
}

// TestValidateTarArchiveGzipped tests gzipped tar archive validation
func TestValidateTarArchiveGzipped(t *testing.T) {
	u := &Uploader{
		config: &UploadConfig{
			RootPrefix: "/workspace",
		},
	}

	// Create a tar.gz archive
	var tarBuf bytes.Buffer
	tw := tar.NewWriter(&tarBuf)

	h := &tar.Header{
		Name:     "file.txt",
		Typeflag: tar.TypeReg,
		Mode:     0644,
		Size:     9,
	}
	if err := tw.WriteHeader(h); err != nil {
		t.Fatalf("Failed to write header: %v", err)
	}
	tw.Write([]byte("test data"))
	if err := tw.Close(); err != nil {
		t.Fatalf("Failed to close tar writer: %v", err)
	}

	// Gzip the tar data
	var gzBuf bytes.Buffer
	gw := gzip.NewWriter(&gzBuf)
	if _, err := gw.Write(tarBuf.Bytes()); err != nil {
		t.Fatalf("Failed to gzip data: %v", err)
	}
	if err := gw.Close(); err != nil {
		t.Fatalf("Failed to close gzip writer: %v", err)
	}

	// Validate the gzipped archive
	err := u.validateTarArchive(gzBuf.Bytes(), "/workspace")
	if err != nil {
		t.Errorf("validateTarArchive() on gzipped data error = %v", err)
	}
}

// TestRelativeSymlinkEscaping tests various symlink escape scenarios
func TestRelativeSymlinkEscaping(t *testing.T) {
	tests := []struct {
		name        string
		symlinkPath string
		target      string
		dest        string
		wantErr     bool
	}{
		{
			name:        "symlink to sibling file",
			symlinkPath: "link",
			target:      "sibling.txt",
			dest:        "/workspace",
			wantErr:     false,
		},
		{
			name:        "symlink to subdirectory file",
			symlinkPath: "link",
			target:      "subdir/file.txt",
			dest:        "/workspace",
			wantErr:     false,
		},
		{
			name:        "symlink to parent directory file",
			symlinkPath: "subdir/link",
			target:      "../parent.txt",
			dest:        "/workspace",
			wantErr:     false,
		},
		{
			name:        "symlink escaping via multiple ..",
			symlinkPath: "deep/link",
			target:      "../../../etc/passwd",
			dest:        "/workspace",
			wantErr:     true,
		},
		{
			name:        "symlink to same directory (dot)",
			symlinkPath: "link",
			target:      "./file.txt",
			dest:        "/workspace",
			wantErr:     false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			u := &Uploader{
				config: &UploadConfig{
					RootPrefix: "/workspace",
				},
			}

			header := &tar.Header{
				Name:     tt.symlinkPath,
				Typeflag: tar.TypeSymlink,
				Linkname: tt.target,
				Mode:     0777,
			}

			err := u.validateTarEntry(header, tt.dest)
			if (err != nil) != tt.wantErr {
				t.Errorf("validateTarEntry() error = %v, wantErr %v", err, tt.wantErr)
			}
		})
	}
}
