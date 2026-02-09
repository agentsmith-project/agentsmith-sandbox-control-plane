package k8s

import (
	"testing"

	v1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/stretchr/testify/assert"
)

func TestHasFinalizer_PodHasFinalizer(t *testing.T) {
	pod := &v1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:       "test-pod",
			Finalizers: []string{"finalizer1", "manager.mbos.io/snapshot", "finalizer2"},
		},
	}

	result := hasFinalizer(pod, "manager.mbos.io/snapshot")

	assert.True(t, result)
}

func TestHasFinalizer_PodDoesNotHaveFinalizer(t *testing.T) {
	pod := &v1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:       "test-pod",
			Finalizers: []string{"finalizer1", "finalizer2"},
		},
	}

	result := hasFinalizer(pod, "manager.mbos.io/snapshot")

	assert.False(t, result)
}

func TestHasFinalizer_EmptyFinalizers(t *testing.T) {
	pod := &v1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:       "test-pod",
			Finalizers: []string{},
		},
	}

	result := hasFinalizer(pod, "manager.mbos.io/snapshot")

	assert.False(t, result)
}

func TestHasFinalizer_NilFinalizers(t *testing.T) {
	pod := &v1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name: "test-pod",
		},
	}

	result := hasFinalizer(pod, "manager.mbos.io/snapshot")

	assert.False(t, result)
}

func TestRemoveFinalizerFromStringSlice_Middle(t *testing.T) {
	finalizers := []string{"finalizer1", "manager.mbos.io/snapshot", "finalizer2"}

	result := removeFinalizerFromStringSlice(finalizers, "manager.mbos.io/snapshot")

	assert.Equal(t, []string{"finalizer1", "finalizer2"}, result)
}

func TestRemoveFinalizerFromStringSlice_First(t *testing.T) {
	finalizers := []string{"manager.mbos.io/snapshot", "finalizer1", "finalizer2"}

	result := removeFinalizerFromStringSlice(finalizers, "manager.mbos.io/snapshot")

	assert.Equal(t, []string{"finalizer1", "finalizer2"}, result)
}

func TestRemoveFinalizerFromStringSlice_Last(t *testing.T) {
	finalizers := []string{"finalizer1", "finalizer2", "manager.mbos.io/snapshot"}

	result := removeFinalizerFromStringSlice(finalizers, "manager.mbos.io/snapshot")

	assert.Equal(t, []string{"finalizer1", "finalizer2"}, result)
}

func TestRemoveFinalizerFromStringSlice_NotFound(t *testing.T) {
	finalizers := []string{"finalizer1", "finalizer2"}

	result := removeFinalizerFromStringSlice(finalizers, "manager.mbos.io/snapshot")

	assert.Equal(t, []string{"finalizer1", "finalizer2"}, result)
}

func TestRemoveFinalizerFromStringSlice_Empty(t *testing.T) {
	finalizers := []string{}

	result := removeFinalizerFromStringSlice(finalizers, "manager.mbos.io/snapshot")

	assert.Equal(t, []string{}, result)
}

func TestRemoveFinalizerFromStringSlice_SingleElement(t *testing.T) {
	finalizers := []string{"manager.mbos.io/snapshot"}

	result := removeFinalizerFromStringSlice(finalizers, "manager.mbos.io/snapshot")

	assert.Equal(t, []string{}, result)
}

func TestRemoveFinalizerFromStringSlice_Duplicates(t *testing.T) {
	finalizers := []string{"finalizer1", "manager.mbos.io/snapshot", "finalizer2", "manager.mbos.io/snapshot"}

	result := removeFinalizerFromStringSlice(finalizers, "manager.mbos.io/snapshot")

	// Should remove all instances
	assert.Equal(t, []string{"finalizer1", "finalizer2"}, result)
}

func TestGetAgentThreadIDFromPod_HasLabel(t *testing.T) {
	pod := &v1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Labels: map[string]string{
				"agent_thread_id": "thread-123",
			},
		},
	}

	result := GetAgentThreadIDFromPod(pod)

	assert.Equal(t, "thread-123", result)
}

func TestGetAgentThreadIDFromPod_NoLabel(t *testing.T) {
	pod := &v1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Labels: map[string]string{
				"other_label": "value",
			},
		},
	}

	result := GetAgentThreadIDFromPod(pod)

	assert.Equal(t, "", result)
}

func TestGetAgentThreadIDFromPod_NilLabels(t *testing.T) {
	pod := &v1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Labels: nil,
		},
	}

	result := GetAgentThreadIDFromPod(pod)

	assert.Equal(t, "", result)
}
