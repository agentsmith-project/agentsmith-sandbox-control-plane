package testutils

import (
	"context"

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

func (m *MockK8sClient) GetPod(ctx context.Context, name string) (*v1.Pod, error) {
	args := m.Called(ctx, name)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(*v1.Pod), args.Error(1)
}

func (m *MockK8sClient) CreatePod(ctx context.Context, pod *v1.Pod) (*v1.Pod, error) {
	args := m.Called(ctx, pod)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(*v1.Pod), args.Error(1)
}

func (m *MockK8sClient) DeletePod(ctx context.Context, name string, options *metav1.DeleteOptions) error {
	args := m.Called(ctx, name, options)
	return args.Error(0)
}

func (m *MockK8sClient) PatchPod(ctx context.Context, name string, pt types.PatchType, data []byte, opts ...interface{}) (*v1.Pod, error) {
	args := m.Called(ctx, name, pt, data, opts)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(*v1.Pod), args.Error(1)
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
