package testutils

import (
	"github.com/google/uuid"
)

// PodBuilder builds test Pod specs.
type PodBuilder struct {
	spec *PodSpec
}

// PodSpec represents a simplified pod specification for testing.
type PodSpec struct {
	Name      string
	Namespace string
	Image     string
	Command   string
	Env       map[string]string
	Labels    map[string]string
}

// NewPodBuilder creates a new PodBuilder.
func NewPodBuilder() *PodBuilder {
	return &PodBuilder{
		spec: &PodSpec{
			Name:      "test-pod-" + uuid.New().String()[:8],
			Namespace: "sandbox-workloads",
			Image:     "test-runner:1.0.0",
			Env:       make(map[string]string),
			Labels:    make(map[string]string),
		},
	}
}

func (b *PodBuilder) WithName(name string) *PodBuilder {
	b.spec.Name = name
	return b
}

func (b *PodBuilder) WithNamespace(namespace string) *PodBuilder {
	b.spec.Namespace = namespace
	return b
}

func (b *PodBuilder) WithImage(image string) *PodBuilder {
	b.spec.Image = image
	return b
}

func (b *PodBuilder) WithCommand(command string) *PodBuilder {
	b.spec.Command = command
	return b
}

func (b *PodBuilder) WithEnv(key, value string) *PodBuilder {
	b.spec.Env[key] = value
	return b
}

func (b *PodBuilder) WithLabel(key, value string) *PodBuilder {
	b.spec.Labels[key] = value
	return b
}

func (b *PodBuilder) Build() *PodSpec {
	return b.spec
}
