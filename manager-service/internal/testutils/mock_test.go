package testutils

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	v1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func TestNotFoundErr(t *testing.T) {
	err := NotFoundErr("pod not found")
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "not found")
}

func TestMockK8sClient_GetPod(t *testing.T) {
	m := &MockK8sClient{}
	pod := &v1.Pod{ObjectMeta: metav1.ObjectMeta{Name: "test"}}
	m.On("GetPod", mock.Anything, "test").Return(pod, nil)

	got, err := m.GetPod(context.Background(), "test")
	assert.NoError(t, err)
	assert.Equal(t, pod, got)
	m.AssertExpectations(t)
}

func TestMockK8sClient_GetPod_NotFound(t *testing.T) {
	m := &MockK8sClient{}
	m.On("GetPod", mock.Anything, "missing").Return((*v1.Pod)(nil), NotFoundErr("pod missing"))

	got, err := m.GetPod(context.Background(), "missing")
	assert.Error(t, err)
	assert.Nil(t, got)
	m.AssertExpectations(t)
}

func TestMockK8sClient_CreatePod(t *testing.T) {
	m := &MockK8sClient{}
	pod := &v1.Pod{ObjectMeta: metav1.ObjectMeta{Name: "new"}}
	m.On("CreatePod", mock.Anything, pod).Return(pod, nil)

	got, err := m.CreatePod(context.Background(), pod)
	assert.NoError(t, err)
	assert.Equal(t, pod, got)
	m.AssertExpectations(t)
}

func TestMockK8sClient_DeletePod(t *testing.T) {
	m := &MockK8sClient{}
	m.On("DeletePod", mock.Anything, "del", (*metav1.DeleteOptions)(nil)).Return(nil)

	err := m.DeletePod(context.Background(), "del", nil)
	assert.NoError(t, err)
	m.AssertExpectations(t)
}

func TestMockK8sClient_PatchPod(t *testing.T) {
	m := &MockK8sClient{}
	patched := &v1.Pod{ObjectMeta: metav1.ObjectMeta{Name: "patched"}}
	m.On("PatchPod", mock.Anything, "p", mock.Anything, mock.Anything, mock.Anything).Return(patched, nil)

	got, err := m.PatchPod(context.Background(), "p", "application/merge-patch+json", []byte(`{}`))
	assert.NoError(t, err)
	assert.Equal(t, patched, got)
	m.AssertExpectations(t)
}
