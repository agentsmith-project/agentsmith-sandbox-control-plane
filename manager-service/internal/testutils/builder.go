package testutils

import (
	"time"

	"github.com/google/uuid"
	"github.com/sandbox/manager/internal/session"
)

// SessionBuilder builds test Session instances.
type SessionBuilder struct {
	s *session.Session
}

// NewSessionBuilder creates a new SessionBuilder.
func NewSessionBuilder() *SessionBuilder {
	return &SessionBuilder{
		s: &session.Session{
			ID:              "test-session-" + uuid.New().String()[:8],
			CreatedAt:       time.Now(),
			State:           session.StateReady,
			ClientConnected: false,
		},
	}
}

// WithID sets the session ID.
func (b *SessionBuilder) WithID(id string) *SessionBuilder {
	b.s.ID = id
	return b
}

// WithAgentThreadID sets the agent thread ID.
func (b *SessionBuilder) WithAgentThreadID(agentThreadID string) *SessionBuilder {
	b.s.AgentThreadID = agentThreadID
	return b
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

// WithState sets the session state.
func (b *SessionBuilder) WithState(state session.SessionState) *SessionBuilder {
	b.s.State = state
	return b
}

// WithImage sets the runner image.
func (b *SessionBuilder) WithImage(image string) *SessionBuilder {
	b.s.Image = image
	return b
}

// WithCreatedAt sets the creation time.
func (b *SessionBuilder) WithCreatedAt(t time.Time) *SessionBuilder {
	b.s.CreatedAt = t
	return b
}

// WithClientConnected sets the client connected flag.
func (b *SessionBuilder) WithClientConnected(connected bool) *SessionBuilder {
	b.s.ClientConnected = connected
	return b
}

// WithLastActivityAt sets the last activity time.
func (b *SessionBuilder) WithLastActivityAt(t time.Time) *SessionBuilder {
	b.s.LastActivityAt = t
	return b
}

// WithIdleTimeout sets the idle timeout duration.
func (b *SessionBuilder) WithIdleTimeout(timeout time.Duration) *SessionBuilder {
	b.s.IdleTimeout = timeout
	return b
}

// WithMaxLifetime sets the maximum lifetime duration.
func (b *SessionBuilder) WithMaxLifetime(lifetime time.Duration) *SessionBuilder {
	b.s.MaxLifetime = lifetime
	return b
}

// Build returns the constructed Session.
func (b *SessionBuilder) Build() *session.Session {
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
