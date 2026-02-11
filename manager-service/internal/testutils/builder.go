package testutils

import (
	"time"

	"github.com/google/uuid"
	"github.com/sandbox/manager/internal/sandbox"
)

// SessionBuilder builds test Sandbox instances.
type SessionBuilder struct {
	s *sandbox.Sandbox
}

// NewSessionBuilder creates a new SessionBuilder.
func NewSessionBuilder() *SessionBuilder {
	now := time.Now()
	return &SessionBuilder{
		s: &sandbox.Sandbox{
			CreatedAt:       now,
			LastActivityAt:  now,
			State:           sandbox.StateReady,
			ClientConnected: false,
			Config: sandbox.SecurityConfig{
				MaxLifetime: sandbox.DefaultMaxLifetime,
				IdleTimeout: 15 * time.Minute,
			},
			ExpiresAt: now.Add(sandbox.DefaultMaxLifetime),
		},
	}
}

// WithSandboxID sets the sandbox ID.
func (b *SessionBuilder) WithSandboxID(sandboxID string) *SessionBuilder {
	b.s.SandboxID = sandboxID
	return b
}

// WithPodName sets the pod name.
func (b *SessionBuilder) WithPodName(podName string) *SessionBuilder {
	b.s.PodName = podName
	return b
}

// WithPodNamespace sets the pod namespace.
func (b *SessionBuilder) WithPodNamespace(podNamespace string) *SessionBuilder {
	b.s.PodNamespace = podNamespace
	return b
}

// WithState sets the session state.
func (b *SessionBuilder) WithState(state sandbox.State) *SessionBuilder {
	b.s.State = state
	return b
}

// WithImage sets the runner image.
func (b *SessionBuilder) WithImage(image string) *SessionBuilder {
	b.s.Image = image
	return b
}

// WithCommand sets the command.
func (b *SessionBuilder) WithCommand(cmd ...string) *SessionBuilder {
	b.s.Command = cmd
	return b
}

// WithEnv sets environment variables.
func (b *SessionBuilder) WithEnv(env map[string]string) *SessionBuilder {
	b.s.Env = env
	return b
}

// WithCreatedAt sets the creation time.
func (b *SessionBuilder) WithCreatedAt(t time.Time) *SessionBuilder {
	b.s.CreatedAt = t
	return b
}

// WithLastActivityAt sets the last activity time.
func (b *SessionBuilder) WithLastActivityAt(t time.Time) *SessionBuilder {
	b.s.LastActivityAt = t
	return b
}

// WithExpiresAt sets the expires at time.
func (b *SessionBuilder) WithExpiresAt(t time.Time) *SessionBuilder {
	b.s.ExpiresAt = t
	return b
}

// WithClientConnected sets the client connected flag.
func (b *SessionBuilder) WithClientConnected(connected bool) *SessionBuilder {
	b.s.ClientConnected = connected
	return b
}

// WithIdleTimeout sets the idle timeout duration.
func (b *SessionBuilder) WithIdleTimeout(timeout time.Duration) *SessionBuilder {
	b.s.Config.IdleTimeout = timeout
	return b
}

// WithMaxLifetime sets the maximum lifetime duration.
func (b *SessionBuilder) WithMaxLifetime(lifetime time.Duration) *SessionBuilder {
	b.s.Config.MaxLifetime = lifetime
	return b
}

// WithAllowNetworkAccess sets network access permission.
func (b *SessionBuilder) WithAllowNetworkAccess(allow bool) *SessionBuilder {
	b.s.Config.AllowNetworkAccess = allow
	return b
}

// WithReadonlyFilesystem sets readonly filesystem.
func (b *SessionBuilder) WithReadonlyFilesystem(readonly bool) *SessionBuilder {
	b.s.Config.ReadonlyFilesystem = readonly
	return b
}

// Build returns the constructed Sandbox.
func (b *SessionBuilder) Build() *sandbox.Sandbox {
	return b.s
}

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
			Namespace: "sandbox",
			Image:     "test-runner:1.0.0",
			Env:       make(map[string]string),
			Labels:    make(map[string]string),
		},
	}
}

// WithName sets the pod name.
func (b *PodBuilder) WithName(name string) *PodBuilder {
	b.spec.Name = name
	return b
}

// WithNamespace sets the namespace.
func (b *PodBuilder) WithNamespace(namespace string) *PodBuilder {
	b.spec.Namespace = namespace
	return b
}

// WithImage sets the image.
func (b *PodBuilder) WithImage(image string) *PodBuilder {
	b.spec.Image = image
	return b
}

// WithCommand sets the command.
func (b *PodBuilder) WithCommand(command string) *PodBuilder {
	b.spec.Command = command
	return b
}

// WithEnv adds an environment variable.
func (b *PodBuilder) WithEnv(key, value string) *PodBuilder {
	b.spec.Env[key] = value
	return b
}

// WithLabel adds a label.
func (b *PodBuilder) WithLabel(key, value string) *PodBuilder {
	b.spec.Labels[key] = value
	return b
}

// Build returns the constructed PodSpec.
func (b *PodBuilder) Build() *PodSpec {
	return b.spec
}
