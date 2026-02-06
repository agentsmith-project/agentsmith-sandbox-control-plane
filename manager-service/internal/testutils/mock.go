package testutils

import (
	"context"
	"io"

	v1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/types"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/stretchr/testify/mock"
)

// MockK8sClient is a mock implementation of K8s client operations.
type MockK8sClient struct {
	mock.Mock
}

// GetPod mocks getting a pod by name.
func (m *MockK8sClient) GetPod(ctx context.Context, name string) (*v1.Pod, error) {
	args := m.Called(ctx, name)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(*v1.Pod), args.Error(1)
}

// CreatePod mocks creating a pod.
func (m *MockK8sClient) CreatePod(ctx context.Context, pod *v1.Pod) (*v1.Pod, error) {
	args := m.Called(ctx, pod)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(*v1.Pod), args.Error(1)
}

// DeletePod mocks deleting a pod.
func (m *MockK8sClient) DeletePod(ctx context.Context, name string, options *metav1.DeleteOptions) error {
	args := m.Called(ctx, name, options)
	return args.Error(0)
}

// PatchPod mocks patching a pod.
func (m *MockK8sClient) PatchPod(ctx context.Context, name string, pt types.PatchType, data []byte, opts ...interface{}) (*v1.Pod, error) {
	args := m.Called(ctx, name, pt, data, opts)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(*v1.Pod), args.Error(1)
}

// MockStorageClient is a mock implementation of Storage client operations.
type MockStorageClient struct {
	mock.Mock
}

// Upload mocks uploading a snapshot.
func (m *MockStorageClient) Upload(ctx context.Context, key string, r io.Reader, size int64) error {
	args := m.Called(ctx, key, r, size)
	return args.Error(0)
}

// Download mocks downloading a snapshot.
func (m *MockStorageClient) Download(ctx context.Context, key string) (io.ReadCloser, int64, error) {
	args := m.Called(ctx, key)
	if args.Get(0) == nil {
		return nil, 0, args.Error(1)
	}
	return args.Get(0).(io.ReadCloser), args.Get(1).(int64), args.Error(2)
}

// Delete mocks deleting a snapshot.
func (m *MockStorageClient) Delete(ctx context.Context, key string) error {
	args := m.Called(ctx, key)
	return args.Error(0)
}

// Exists mocks checking if a snapshot exists.
func (m *MockStorageClient) Exists(ctx context.Context, key string) (bool, error) {
	args := m.Called(ctx, key)
	return args.Bool(0), args.Error(1)
}

// NotFoundErr creates a not found error for testing.
func NotFoundErr(msg string) error {
	return &errors.StatusError{
		ErrStatus: metav1.Status{
			Status:  metav1.StatusFailure,
			Reason:  metav1.StatusReasonNotFound,
			Message: msg,
			Code:    404,
		},
	}
}
