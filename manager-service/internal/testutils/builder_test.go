package testutils

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestNewPodBuilder(t *testing.T) {
	b := NewPodBuilder()
	require.NotNil(t, b)
}

func TestPodBuilder_WithName(t *testing.T) {
	b := NewPodBuilder().WithName("my-pod")
	require.NotNil(t, b)
	spec := b.Build()
	assert.Equal(t, "my-pod", spec.Name)
}

func TestPodBuilder_WithNamespace(t *testing.T) {
	b := NewPodBuilder().WithNamespace("my-ns")
	spec := b.Build()
	assert.Equal(t, "my-ns", spec.Namespace)
}

func TestPodBuilder_WithImage(t *testing.T) {
	b := NewPodBuilder().WithImage("nginx:latest")
	spec := b.Build()
	assert.Equal(t, "nginx:latest", spec.Image)
}

func TestPodBuilder_WithCommand(t *testing.T) {
	b := NewPodBuilder().WithCommand("sleep 3600")
	spec := b.Build()
	assert.Equal(t, "sleep 3600", spec.Command)
}

func TestPodBuilder_WithEnv(t *testing.T) {
	b := NewPodBuilder().WithEnv("KEY", "value")
	spec := b.Build()
	require.NotNil(t, spec.Env)
	assert.Equal(t, "value", spec.Env["KEY"])
}

func TestPodBuilder_WithLabel(t *testing.T) {
	b := NewPodBuilder().WithLabel("app", "test")
	spec := b.Build()
	require.NotNil(t, spec.Labels)
	assert.Equal(t, "test", spec.Labels["app"])
}

func TestPodBuilder_Chained(t *testing.T) {
	spec := NewPodBuilder().
		WithName("chained").
		WithNamespace("ns").
		WithImage("img").
		WithEnv("E", "1").
		WithLabel("l", "v").
		Build()
	assert.Equal(t, "chained", spec.Name)
	assert.Equal(t, "ns", spec.Namespace)
	assert.Equal(t, "img", spec.Image)
	assert.Equal(t, "1", spec.Env["E"])
	assert.Equal(t, "v", spec.Labels["l"])
}
