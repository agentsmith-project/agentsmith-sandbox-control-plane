package httpapi

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/sandbox/manager/internal/config"
	"github.com/sandbox/manager/internal/k8s"
	"github.com/sandbox/manager/internal/observability"
	"github.com/sandbox/manager/internal/storage"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// ---------------------------------------------------------------------------
// mockManager – implements Manager for unit tests.
// Returns a real *config.Config and *observability.MetricsRegistry, but nil
// for K8s client/executor since we only exercise validation paths.
// ---------------------------------------------------------------------------

type mockManager struct {
	cfg     *config.Config
	metrics *observability.MetricsRegistry
}

func newMockManager() *mockManager {
	return &mockManager{
		cfg:     config.DefaultConfig(),
		metrics: observability.NewMetricsRegistry(),
	}
}

func (m *mockManager) GetConfig() *config.Config                   { return m.cfg }
func (m *mockManager) GetK8sClient() *k8s.Client                   { return nil }
func (m *mockManager) GetK8sExecutor() *k8s.Executor               { return nil }
func (m *mockManager) GetMetrics() *observability.MetricsRegistry   { return m.metrics }
func (m *mockManager) GetStorageClient() *storage.Client            { return nil }

// parseErrorResponse decodes an ErrorResponse from the recorder body.
func parseErrorResponse(t *testing.T, rec *httptest.ResponseRecorder) ErrorResponse {
	t.Helper()
	var resp ErrorResponse
	err := json.NewDecoder(rec.Body).Decode(&resp)
	require.NoError(t, err, "failed to decode error response body")
	return resp
}

// ===========================================================================
// extractSessionId – pure function, no dependencies
// ===========================================================================

func TestExtractSessionId(t *testing.T) {
	tests := []struct {
		name string
		path string
		want string
	}{
		{"basic session id", "/v1/sandboxes/abc123", "abc123"},
		{"with /exec suffix", "/v1/sandboxes/abc123/exec", "abc123"},
		{"with /touch suffix", "/v1/sandboxes/abc123/touch", "abc123"},
		{"with /files/upload suffix", "/v1/sandboxes/abc123/files/upload", "abc123"},
		{"trailing slash – empty session", "/v1/sandboxes/", ""},
		{"no trailing slash – too few parts", "/v1/sandboxes", ""},
		{"wrong version prefix", "/v2/sandboxes/abc", ""},
		{"wrong resource segment", "/v1/other/abc", ""},
		{"empty path", "", ""},
		{"root path", "/", ""},
		// format validation
		{"uuid-like id", "/v1/sandboxes/550e8400-e29b-41d4-a716-446655440000", "550e8400-e29b-41d4-a716-446655440000"},
		{"dotted id", "/v1/sandboxes/my.session.v1", "my.session.v1"},
		{"underscored id", "/v1/sandboxes/my_session_1", "my_session_1"},
		{"rejects path traversal", "/v1/sandboxes/../etc", ""},
		{"rejects slash encoded", "/v1/sandboxes/foo;bar", ""},
		{"rejects starting with hyphen", "/v1/sandboxes/-badstart", ""},
		{"rejects starting with dot", "/v1/sandboxes/.hidden", ""},
		{"rejects space", "/v1/sandboxes/has space", ""},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := extractSessionId(tt.path)
			assert.Equal(t, tt.want, got)
		})
	}
}

func TestIsValidSessionId(t *testing.T) {
	tests := []struct {
		name  string
		id    string
		valid bool
	}{
		{"simple alphanumeric", "abc123", true},
		{"uuid", "550e8400-e29b-41d4-a716-446655440000", true},
		{"dots allowed", "session.v1.2", true},
		{"underscores allowed", "my_session_1", true},
		{"max length", strings.Repeat("a", maxSessionIdLen), true},
		{"too long", strings.Repeat("a", maxSessionIdLen+1), false},
		{"empty", "", false},
		{"starts with hyphen", "-bad", false},
		{"starts with dot", ".bad", false},
		{"starts with underscore", "_bad", false},
		{"contains slash", "foo/bar", false},
		{"contains backslash", "foo\\bar", false},
		{"contains space", "foo bar", false},
		{"contains semicolon", "foo;bar", false},
		{"path traversal", "../etc", false},
		{"single char", "a", true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.valid, isValidSessionId(tt.id))
		})
	}
}

// ===========================================================================
// buildPodSpec – depends only on config, no K8s needed
// ===========================================================================

func TestBuildPodSpec(t *testing.T) {
	mgr := newMockManager()
	h := NewHandlers(mgr)
	cfg := mgr.cfg

	t.Run("defaults when request has no overrides", func(t *testing.T) {
		spec := h.buildPodSpec("sess-1", &CreateSandboxRequest{})

		assert.Equal(t, "sess-1", spec.SessionID)
		assert.Equal(t, cfg.Sandbox.Defaults.RunnerImage, spec.Image)
		assert.Equal(t, cfg.Sandbox.Defaults.TTLSeconds, spec.TTLSeconds)
		assert.Equal(t, cfg.Sandbox.Defaults.Resources.Limits.CPU, spec.CPULimit)
		assert.Equal(t, cfg.Sandbox.Defaults.Resources.Limits.Memory, spec.MemoryLimit)
		assert.Equal(t, cfg.Sandbox.Defaults.Resources.Limits.EphemeralStorage, spec.EphemeralStorageLimit)
		assert.Equal(t, cfg.Sandbox.Defaults.ContainerName, spec.ContainerName)
		assert.Equal(t, cfg.Sandbox.Defaults.Workdir, spec.Workdir)
		assert.Nil(t, spec.Env)
		assert.Equal(t, cfg.Sandbox.Defaults.ImagePullPolicy, spec.ImagePullPolicy)

		// Resource requests come from config
		assert.Equal(t, cfg.Sandbox.Defaults.Resources.Requests.CPU, spec.ResourceRequests.CPU)
		assert.Equal(t, cfg.Sandbox.Defaults.Resources.Requests.Memory, spec.ResourceRequests.Memory)

		// Resource limits mirror the resolved limit values
		assert.Equal(t, spec.CPULimit, spec.ResourceLimits.CPU)
		assert.Equal(t, spec.MemoryLimit, spec.ResourceLimits.Memory)
		assert.Equal(t, spec.EphemeralStorageLimit, spec.ResourceLimits.EphemeralStorage)

		// Security context
		require.NotNil(t, spec.SecurityContext)
		assert.True(t, spec.SecurityContext.NonRoot)
		assert.Equal(t, int64(10001), spec.SecurityContext.RunAsUser)
		assert.True(t, spec.SecurityContext.DropAllCapabilities)
		assert.False(t, spec.SecurityContext.ReadOnlyRoot)
	})

	t.Run("custom image, TTL, CPU, memory, workdir, env (limits clamped to config max)", func(t *testing.T) {
		req := &CreateSandboxRequest{
			Image:                 "custom:latest",
			TTLSeconds:            600,
			CPULimit:              "2",
			MemoryLimit:           "4Gi",
			EphemeralStorageLimit: "10Gi",
			ContainerName:         "my-container",
			Workdir:               "/workspace/project",
			Env:                   map[string]string{"MY_VAR": "value"},
		}
		spec := h.buildPodSpec("sess-2", req)

		assert.Equal(t, "sess-2", spec.SessionID)
		assert.Equal(t, "custom:latest", spec.Image)
		assert.Equal(t, 600, spec.TTLSeconds) // 600 <= config 900, unchanged
		// Config max is CPU "1", Memory 1Gi, EphemeralStorage 2Gi — request values are clamped
		assert.Equal(t, "1", spec.CPULimit)
		assert.Equal(t, "1Gi", spec.MemoryLimit)
		assert.Equal(t, "2Gi", spec.EphemeralStorageLimit)
		assert.Equal(t, "my-container", spec.ContainerName)
		assert.Equal(t, "/workspace/project", spec.Workdir)
		assert.Equal(t, map[string]string{"MY_VAR": "value"}, spec.Env)
	})

	t.Run("labels and annotations from config are merged", func(t *testing.T) {
		spec := h.buildPodSpec("sess-3", &CreateSandboxRequest{})

		// DefaultConfig sets Labels: {"app": "llm-sandbox"}
		require.Contains(t, spec.Labels, "app")
		assert.Equal(t, "llm-sandbox", spec.Labels["app"])

		// DefaultConfig sets Annotations to an empty map – should still be non-nil
		assert.NotNil(t, spec.Annotations)
	})

	t.Run("volumes from config are included", func(t *testing.T) {
		spec := h.buildPodSpec("sess-4", &CreateSandboxRequest{})

		assert.Len(t, spec.Volumes, len(cfg.Sandbox.Defaults.Volumes))

		names := make(map[string]bool)
		for _, v := range spec.Volumes {
			names[v.Name] = true
		}
		assert.True(t, names["workspace"], "expected workspace volume")
		assert.True(t, names["tmp"], "expected tmp volume")
	})
}

// ===========================================================================
// HandleCreateSandbox – validation paths (before K8s calls)
// ===========================================================================

func TestHandleCreateSandbox_Validation(t *testing.T) {
	mgr := newMockManager()
	h := NewHandlers(mgr)

	t.Run("missing sessionId returns 400", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodPut, "/v1/sandboxes/", strings.NewReader("{}"))
		rec := httptest.NewRecorder()
		h.HandleCreateSandbox(rec, req)

		assert.Equal(t, http.StatusBadRequest, rec.Code)
		resp := parseErrorResponse(t, rec)
		assert.Equal(t, string(ErrBadRequest), resp.Error.Code)
		assert.Contains(t, resp.Error.Message, "sessionId")
	})

	t.Run("invalid JSON body returns 400", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodPut, "/v1/sandboxes/abc123", strings.NewReader("{invalid"))
		rec := httptest.NewRecorder()
		h.HandleCreateSandbox(rec, req)

		assert.Equal(t, http.StatusBadRequest, rec.Code)
		resp := parseErrorResponse(t, rec)
		assert.Equal(t, string(ErrBadRequest), resp.Error.Code)
	})

	t.Run("invalid env key returns 422", func(t *testing.T) {
		body := `{"env":{"foo-bar":"v"}}`
		req := httptest.NewRequest(http.MethodPut, "/v1/sandboxes/abc123", strings.NewReader(body))
		rec := httptest.NewRecorder()
		h.HandleCreateSandbox(rec, req)

		assert.Equal(t, http.StatusUnprocessableEntity, rec.Code)
		resp := parseErrorResponse(t, rec)
		assert.Equal(t, string(ErrInvalidEnvKey), resp.Error.Code)
		assert.Contains(t, resp.Error.Message, "foo-bar")
	})

	t.Run("invalid workdir returns 422", func(t *testing.T) {
		body := `{"workdir":"/etc"}`
		req := httptest.NewRequest(http.MethodPut, "/v1/sandboxes/abc123", strings.NewReader(body))
		rec := httptest.NewRecorder()
		h.HandleCreateSandbox(rec, req)

		assert.Equal(t, http.StatusUnprocessableEntity, rec.Code)
		resp := parseErrorResponse(t, rec)
		assert.Equal(t, string(ErrInvalidWorkdir), resp.Error.Code)
		assert.Contains(t, resp.Error.Message, "/etc")
	})
}

// ===========================================================================
// HandleExec – validation paths (before K8s calls)
// ===========================================================================

func TestHandleExec_Validation(t *testing.T) {
	mgr := newMockManager()
	h := NewHandlers(mgr)

	t.Run("missing sessionId returns 400", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodPost, "/v1/sandboxes/", strings.NewReader("{}"))
		rec := httptest.NewRecorder()
		h.HandleExec(rec, req)

		assert.Equal(t, http.StatusBadRequest, rec.Code)
		resp := parseErrorResponse(t, rec)
		assert.Equal(t, string(ErrBadRequest), resp.Error.Code)
	})

	t.Run("invalid JSON body returns 400", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodPost, "/v1/sandboxes/abc123/exec", strings.NewReader("not-json"))
		rec := httptest.NewRecorder()
		h.HandleExec(rec, req)

		assert.Equal(t, http.StatusBadRequest, rec.Code)
		resp := parseErrorResponse(t, rec)
		assert.Equal(t, string(ErrBadRequest), resp.Error.Code)
	})

	t.Run("empty cmd returns 400", func(t *testing.T) {
		body := `{"cmd":[]}`
		req := httptest.NewRequest(http.MethodPost, "/v1/sandboxes/abc123/exec", strings.NewReader(body))
		rec := httptest.NewRecorder()
		h.HandleExec(rec, req)

		assert.Equal(t, http.StatusBadRequest, rec.Code)
		resp := parseErrorResponse(t, rec)
		assert.Equal(t, string(ErrBadRequest), resp.Error.Code)
		assert.Contains(t, resp.Error.Message, "cmd")
	})

	t.Run("invalid env key returns 422", func(t *testing.T) {
		body := `{"cmd":["echo","hi"],"env":{"foo-bar":"v"}}`
		req := httptest.NewRequest(http.MethodPost, "/v1/sandboxes/abc123/exec", strings.NewReader(body))
		rec := httptest.NewRecorder()
		h.HandleExec(rec, req)

		assert.Equal(t, http.StatusUnprocessableEntity, rec.Code)
		resp := parseErrorResponse(t, rec)
		assert.Equal(t, string(ErrInvalidEnvKey), resp.Error.Code)
	})

	t.Run("invalid workdir returns 422", func(t *testing.T) {
		body := `{"cmd":["echo","hi"],"workdir":"/etc"}`
		req := httptest.NewRequest(http.MethodPost, "/v1/sandboxes/abc123/exec", strings.NewReader(body))
		rec := httptest.NewRecorder()
		h.HandleExec(rec, req)

		assert.Equal(t, http.StatusUnprocessableEntity, rec.Code)
		resp := parseErrorResponse(t, rec)
		assert.Equal(t, string(ErrInvalidWorkdir), resp.Error.Code)
	})

	t.Run("timeout exceeds max returns 504", func(t *testing.T) {
		// NOTE: The timeout check in HandleExec is positioned AFTER
		// ensurePodReady and GetPod (K8s calls), so it cannot be reached
		// with a nil k8sClient. This test documents the expected behaviour
		// and should be enabled once K8s client mocking is available.
		t.Skip("timeout validation occurs after K8s calls; requires K8s client mock")
	})
}

// ===========================================================================
// HandleTouch – validation paths
// ===========================================================================

func TestHandleTouch_Validation(t *testing.T) {
	mgr := newMockManager()
	h := NewHandlers(mgr)

	t.Run("missing sessionId returns 400", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodPost, "/v1/sandboxes/", nil)
		rec := httptest.NewRecorder()
		h.HandleTouch(rec, req)

		assert.Equal(t, http.StatusBadRequest, rec.Code)
		resp := parseErrorResponse(t, rec)
		assert.Equal(t, string(ErrBadRequest), resp.Error.Code)
		assert.Contains(t, resp.Error.Message, "sessionId")
	})
}

// ===========================================================================
// HandleUpload – validation paths (before K8s calls)
// ===========================================================================

func TestHandleUpload_Validation(t *testing.T) {
	mgr := newMockManager()
	h := NewHandlers(mgr)

	t.Run("missing sessionId returns 400", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodPost, "/v1/sandboxes/", strings.NewReader("data"))
		rec := httptest.NewRecorder()
		h.HandleUpload(rec, req)

		assert.Equal(t, http.StatusBadRequest, rec.Code)
		resp := parseErrorResponse(t, rec)
		assert.Equal(t, string(ErrBadRequest), resp.Error.Code)
	})

	t.Run("invalid dest path returns 422", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodPost,
			"/v1/sandboxes/abc123/files/upload?dest=/etc/passwd",
			strings.NewReader("data"))
		rec := httptest.NewRecorder()
		h.HandleUpload(rec, req)

		assert.Equal(t, http.StatusUnprocessableEntity, rec.Code)
		resp := parseErrorResponse(t, rec)
		assert.Equal(t, string(ErrInvalidPath), resp.Error.Code)
		assert.Contains(t, resp.Error.Message, "/etc/passwd")
	})
}

// ===========================================================================
// HandleDownload – validation paths (before K8s calls)
// ===========================================================================

func TestHandleDownload_Validation(t *testing.T) {
	mgr := newMockManager()
	h := NewHandlers(mgr)

	t.Run("missing sessionId returns 400", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/v1/sandboxes/", nil)
		rec := httptest.NewRecorder()
		h.HandleDownload(rec, req)

		assert.Equal(t, http.StatusBadRequest, rec.Code)
		resp := parseErrorResponse(t, rec)
		assert.Equal(t, string(ErrBadRequest), resp.Error.Code)
	})

	t.Run("invalid src path returns 422", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet,
			"/v1/sandboxes/abc123/files/download?src=/etc/passwd", nil)
		rec := httptest.NewRecorder()
		h.HandleDownload(rec, req)

		assert.Equal(t, http.StatusUnprocessableEntity, rec.Code)
		resp := parseErrorResponse(t, rec)
		assert.Equal(t, string(ErrInvalidPath), resp.Error.Code)
		assert.Contains(t, resp.Error.Message, "/etc/passwd")
	})
}

// ===========================================================================
// HandleDelete – validation paths
// ===========================================================================

func TestHandleDelete_Validation(t *testing.T) {
	mgr := newMockManager()
	h := NewHandlers(mgr)

	t.Run("missing sessionId returns 400", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodDelete, "/v1/sandboxes/", nil)
		rec := httptest.NewRecorder()
		h.HandleDelete(rec, req)

		assert.Equal(t, http.StatusBadRequest, rec.Code)
		resp := parseErrorResponse(t, rec)
		assert.Equal(t, string(ErrBadRequest), resp.Error.Code)
	})
}
