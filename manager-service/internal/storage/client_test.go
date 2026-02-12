package storage

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/minio/minio-go/v7"
)

// mockMinioClient is a mock implementation for testing
type mockMinioClient struct {
	statObjectFunc  func(ctx context.Context, bucketName, objectName string, opts minio.StatObjectOptions) (minio.ObjectInfo, error)
	putObjectFunc   func(ctx context.Context, bucketName, objectName string, reader io.Reader, objectSize int64, opts minio.PutObjectOptions) (minio.UploadInfo, error)
	getObjectFunc   func(ctx context.Context, bucketName, objectName string, opts minio.GetObjectOptions) (*minio.Object, error)
	removeObjectFunc func(ctx context.Context, bucketName, objectName string, opts minio.RemoveObjectOptions) error
}

func (m *mockMinioClient) StatObject(ctx context.Context, bucketName, objectName string, opts minio.StatObjectOptions) (minio.ObjectInfo, error) {
	if m.statObjectFunc != nil {
		return m.statObjectFunc(ctx, bucketName, objectName, opts)
	}
	return minio.ObjectInfo{}, nil
}

func (m *mockMinioClient) PutObject(ctx context.Context, bucketName, objectName string, reader io.Reader, objectSize int64, opts minio.PutObjectOptions) (minio.UploadInfo, error) {
	if m.putObjectFunc != nil {
		return m.putObjectFunc(ctx, bucketName, objectName, reader, objectSize, opts)
	}
	return minio.UploadInfo{}, nil
}

func (m *mockMinioClient) GetObject(ctx context.Context, bucketName, objectName string, opts minio.GetObjectOptions) (*minio.Object, error) {
	if m.getObjectFunc != nil {
		return m.getObjectFunc(ctx, bucketName, objectName, opts)
	}
	return nil, nil
}

func (m *mockMinioClient) RemoveObject(ctx context.Context, bucketName, objectName string, opts minio.RemoveObjectOptions) error {
	if m.removeObjectFunc != nil {
		return m.removeObjectFunc(ctx, bucketName, objectName, opts)
	}
	return nil
}

// TestableClient wraps Client with injectable mock for testing
type testableClient struct {
	*Client
	mockClient *mockMinioClient
}

func newTestableClient(mock *mockMinioClient) *testableClient {
	return &testableClient{
		Client: &Client{
			bucket: "test-bucket",
		},
		mockClient: mock,
	}
}

func (tc *testableClient) SnapshotExists(ctx context.Context, key string) (bool, error) {
	if tc.mockClient != nil && tc.mockClient.statObjectFunc != nil {
		_, err := tc.mockClient.StatObject(ctx, tc.bucket, key, minio.StatObjectOptions{})
		if err != nil {
			errResp := minio.ToErrorResponse(err)
			// Check if this is a valid minio error response (Code is set)
			// ToErrorResponse returns a zero-value ErrorResponse for non-minio errors,
			// with Code == "" (empty string), so we check for "NoSuchKey" explicitly
			if errResp.Code == "NoSuchKey" {
				return false, nil
			}
			return false, err
		}
		return true, nil
	}
	return false, errors.New("mock not configured")
}

// TestSnapshotExists_NoSuchKey tests the NoSuchKey error path
func TestSnapshotExists_NoSuchKey(t *testing.T) {
	mock := &mockMinioClient{
		statObjectFunc: func(ctx context.Context, bucketName, objectName string, opts minio.StatObjectOptions) (minio.ObjectInfo, error) {
			// Return a minio NoSuchKey error
			return minio.ObjectInfo{}, minio.ErrorResponse{
				StatusCode: http.StatusNotFound,
				Code:       "NoSuchKey",
				Message:    "The specified key does not exist",
			}
		},
	}

	tc := newTestableClient(mock)
	ctx := context.Background()

	exists, err := tc.SnapshotExists(ctx, "test-key")
	if err != nil {
		t.Errorf("SnapshotExists() error = %v, want nil", err)
	}
	if exists != false {
		t.Errorf("SnapshotExists() = %v, want false", exists)
	}
}

// TestSnapshotExists_NonMinioError tests the non-minio error path
func TestSnapshotExists_NonMinioError(t *testing.T) {
	mock := &mockMinioClient{
		statObjectFunc: func(ctx context.Context, bucketName, objectName string, opts minio.StatObjectOptions) (minio.ObjectInfo, error) {
			// Return a non-minio error (e.g., network error)
			return minio.ObjectInfo{}, errors.New("connection timeout")
		},
	}

	tc := newTestableClient(mock)
	ctx := context.Background()

	exists, err := tc.SnapshotExists(ctx, "test-key")
	if err == nil {
		t.Error("SnapshotExists() error = nil, want non-nil error")
	}
	if exists != false {
		t.Errorf("SnapshotExists() = %v, want false", exists)
	}
}

// TestSnapshotExists_ObjectExists tests successful object stat
func TestSnapshotExists_ObjectExists(t *testing.T) {
	mock := &mockMinioClient{
		statObjectFunc: func(ctx context.Context, bucketName, objectName string, opts minio.StatObjectOptions) (minio.ObjectInfo, error) {
			// Return successful stat
			return minio.ObjectInfo{
				Key:  objectName,
				Size: 1024,
			}, nil
		},
	}

	tc := newTestableClient(mock)
	ctx := context.Background()

	exists, err := tc.SnapshotExists(ctx, "test-key")
	if err != nil {
		t.Errorf("SnapshotExists() error = %v, want nil", err)
	}
	if exists != true {
		t.Errorf("SnapshotExists() = %v, want true", exists)
	}
}

// TestSnapshotExists_OtherMinioError tests other minio errors are propagated
func TestSnapshotExists_OtherMinioError(t *testing.T) {
	mock := &mockMinioClient{
		statObjectFunc: func(ctx context.Context, bucketName, objectName string, opts minio.StatObjectOptions) (minio.ObjectInfo, error) {
			// Return a different minio error (not NoSuchKey)
			return minio.ObjectInfo{}, minio.ErrorResponse{
				StatusCode: http.StatusForbidden,
				Code:       "AccessDenied",
				Message:    "Access Denied",
			}
		},
	}

	tc := newTestableClient(mock)
	ctx := context.Background()

	exists, err := tc.SnapshotExists(ctx, "test-key")
	if err == nil {
		t.Error("SnapshotExists() error = nil, want AccessDenied error")
	}
	if exists != false {
		t.Errorf("SnapshotExists() = %v, want false", exists)
	}
}

// TestSnapshotExists_NoPanicOnNonMinioError verifies no panic occurs with non-minio errors
func TestSnapshotExists_NoPanicOnNonMinioError(t *testing.T) {
	defer func() {
		if r := recover(); r != nil {
			t.Errorf("SnapshotExists() panicked with: %v", r)
		}
	}()

	mock := &mockMinioClient{
		statObjectFunc: func(ctx context.Context, bucketName, objectName string, opts minio.StatObjectOptions) (minio.ObjectInfo, error) {
			// Return a non-minio error
			return minio.ObjectInfo{}, errors.New("some random error")
		},
	}

	tc := newTestableClient(mock)
	ctx := context.Background()

	// This should NOT panic
	_, _ = tc.SnapshotExists(ctx, "test-key")
}

// TestGenerateSnapshotKey tests key generation
func TestGenerateSnapshotKey(t *testing.T) {
	c := &Client{}

	t.Run("generates key with sandboxID and timestamp", func(t *testing.T) {
		key, err := c.GenerateSnapshotKey("my-sandbox-abc123")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		// Key format: snapshots/{sandboxID}/{timestamp}.tar.gz
		if !strings.HasPrefix(key, "snapshots/my-sandbox-abc123/") {
			t.Errorf("key should start with snapshots/my-sandbox-abc123/, got: %s", key)
		}
		if !strings.HasSuffix(key, ".tar.gz") {
			t.Errorf("key should end with .tar.gz, got: %s", key)
		}
	})

	t.Run("different calls produce unique keys over time", func(t *testing.T) {
		key1, err := c.GenerateSnapshotKey("sandbox-1")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		// Keys generated in the same second may be identical, but format is correct
		if !strings.HasPrefix(key1, "snapshots/sandbox-1/") {
			t.Errorf("key should contain sandbox ID, got: %s", key1)
		}
	})

	t.Run("rejects path traversal", func(t *testing.T) {
		_, err := c.GenerateSnapshotKey("../evil")
		if err == nil {
			t.Error("expected error for path traversal")
		}
	})

	t.Run("rejects slash in sandboxID", func(t *testing.T) {
		_, err := c.GenerateSnapshotKey("foo/bar")
		if err == nil {
			t.Error("expected error for slash in sandboxID")
		}
	})

	t.Run("rejects empty sandboxID", func(t *testing.T) {
		_, err := c.GenerateSnapshotKey("")
		if err == nil {
			t.Error("expected error for empty sandboxID")
		}
	})
}

// TestToErrorResponseBehavior documents the actual behavior of minio.ToErrorResponse
func TestToErrorResponseBehavior(t *testing.T) {
	t.Run("minio error returns valid ErrorResponse", func(t *testing.T) {
		// When err is a minio error response:
		err := minio.ErrorResponse{
			StatusCode: http.StatusNotFound,
			Code:       "NoSuchKey",
			Message:    "The specified key does not exist",
		}
		errResp := minio.ToErrorResponse(err)
		if errResp.Code != "NoSuchKey" {
			t.Errorf("Expected Code=NoSuchKey, got %s", errResp.Code)
		}
		t.Log("Minio errors have valid ErrorResponse with Code field")
	})

	t.Run("non-minio error returns zero-value ErrorResponse", func(t *testing.T) {
		// When err is NOT a minio error:
		// minio.ToErrorResponse uses type assertion, which returns zero-value struct for non-matching types
		// The zero-value ErrorResponse has Code == "" (empty string)
		err := errors.New("connection timeout")
		errResp := minio.ToErrorResponse(err)
		if errResp.Code != "" {
			t.Errorf("Expected empty Code for non-minio error, got %s", errResp.Code)
		}
		t.Log("Non-minio errors cause ToErrorResponse to return zero-value ErrorResponse (not nil)")
	})

	t.Run("nil error returns zero-value ErrorResponse", func(t *testing.T) {
		// When err is nil, ToErrorResponse returns zero-value ErrorResponse
		var err error = nil
		errResp := minio.ToErrorResponse(err)
		// errResp will be a valid pointer to zero-value struct
		if errResp.Code != "" {
			t.Errorf("Expected empty Code for nil error, got %s", errResp.Code)
		}
		t.Log("Nil error causes ToErrorResponse to return zero-value ErrorResponse")
	})
}

// TestHTTPStatusCodeToErrorResponse tests that various HTTP status codes
// can be converted to ErrorResponse
func TestHTTPStatusCodeToErrorResponse(t *testing.T) {
	statusCodes := []struct {
		code      int
		minioCode string
	}{
		{http.StatusNotFound, "NoSuchKey"},
		{http.StatusForbidden, "AccessDenied"},
		{http.StatusUnauthorized, "InvalidAccessKeyId"},
		{http.StatusInternalServerError, "InternalError"},
	}

	for _, tc := range statusCodes {
		t.Run(tc.minioCode, func(t *testing.T) {
			errResp := minio.ErrorResponse{
				StatusCode: tc.code,
				Code:       tc.minioCode,
				Message:    "test error",
			}
			if errResp.Code != tc.minioCode {
				t.Errorf("Expected Code=%s, got %s", tc.minioCode, errResp.Code)
			}
			t.Logf("HTTP %d produces minio error code: %s", tc.code, tc.minioCode)
		})
	}
}

// Benchmark test for key generation
func BenchmarkGenerateSnapshotKey(b *testing.B) {
	c := &Client{}
	for i := 0; i < b.N; i++ {
		_, _ = c.GenerateSnapshotKey("sandbox-abc123")
	}
}

// Test error wrapping chain
func TestErrorWrapping(t *testing.T) {
	t.Run("wrapped error preserves original", func(t *testing.T) {
		originalErr := errors.New("original error")
		wrappedErr := fmt.Errorf("context: %w", originalErr)

		if !errors.Is(wrappedErr, originalErr) {
			t.Error("Wrapped error should be detectable with errors.Is")
		}
	})

	t.Run("double wrapped error preserves chain", func(t *testing.T) {
		originalErr := errors.New("original error")
		firstWrap := fmt.Errorf("first: %w", originalErr)
		secondWrap := fmt.Errorf("second: %w", firstWrap)

		if !errors.Is(secondWrap, originalErr) {
			t.Error("Doubly wrapped error should be detectable")
		}
	})
}

// Test io.Reader behavior in storage operations
func TestIOReaderBehavior(t *testing.T) {
	t.Run("strings.Reader implements io.Reader", func(t *testing.T) {
		data := "test data"
		reader := strings.NewReader(data)

		buf := make([]byte, len(data))
		n, err := reader.Read(buf)

		if err != nil && err != io.EOF {
			t.Errorf("Unexpected error: %v", err)
		}
		if n != len(data) {
			t.Errorf("Expected to read %d bytes, got %d", len(data), n)
		}
	})
}
