package httpapi

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"strings"
	"time"

	"github.com/sandbox/manager/internal/auth"
	"github.com/sandbox/manager/internal/config"
	"github.com/sandbox/manager/internal/exec"
	"github.com/sandbox/manager/internal/files"
	"github.com/sandbox/manager/internal/k8s"
	"github.com/sandbox/manager/internal/observability"
	"github.com/sandbox/manager/internal/validation"
)

// Manager is the interface for handlers to interact with the service
type Manager interface {
	GetConfig() *config.Config
	GetK8sClient() *k8s.Client
	GetK8sExecutor() *k8s.Executor
	GetMetrics() *observability.MetricsRegistry
	GetAuthorizer() *auth.Authorizer
}

// Handlers contains all the HTTP handlers
type Handlers struct {
	mgr         Manager
	cfg         *config.Config
	k8sClient   *k8s.Client
	k8sExecutor *k8s.Executor
	metrics     *observability.MetricsRegistry
	authorizer  *auth.Authorizer
}

// NewHandlers creates a new handlers instance
func NewHandlers(mgr Manager) *Handlers {
	return &Handlers{
		mgr:         mgr,
		cfg:         mgr.GetConfig(),
		k8sClient:   mgr.GetK8sClient(),
		k8sExecutor: mgr.GetK8sExecutor(),
		metrics:     mgr.GetMetrics(),
		authorizer:  mgr.GetAuthorizer(),
	}
}

// HandleCreateSandbox handles PUT /v1/sandboxes/{sessionId}
func (h *Handlers) HandleCreateSandbox(w http.ResponseWriter, r *http.Request) {
	sessionId := extractSessionId(r.URL.Path)
	if sessionId == "" {
		WriteError(w, r, ErrBadRequest, "sessionId required")
		return
	}

	var req CreateSandboxRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		WriteErrorWithCause(w, r, ErrBadRequest, "Invalid request body", err)
		return
	}
	// Close the request body to prevent resource leaks
	defer r.Body.Close()

	// Validate environment variable keys
	for key := range req.Env {
		if !h.cfg.ValidateEnvKey(key) {
			WriteError(w, r, ErrInvalidEnvKey, fmt.Sprintf("Invalid environment variable key: %s", key))
			return
		}
	}

	// Validate workdir if provided
	if req.Workdir != "" && !h.cfg.ValidateWorkdir(req.Workdir) {
		WriteError(w, r, ErrInvalidWorkdir, fmt.Sprintf("Invalid workdir: %s", req.Workdir))
		return
	}

	ctx := r.Context()
	podSpec := h.buildPodSpec(sessionId, &req)

	result, err := h.k8sClient.EnsurePod(
		ctx,
		podSpec,
		h.cfg.Sandbox.Defaults.PodReadyWait,
		h.cfg.Sandbox.Defaults.PodPollInterval,
	)
	if err != nil {
		h.metrics.RecordK8sAPIFailure("EnsurePod")
		if isContextCanceled(err) {
			log.Printf("[DEBUG] CreateSandbox canceled for session %s: %v", sessionId, err)
		} else {
			log.Printf("[ERROR] Failed to ensure pod for session %s: %v", sessionId, err)
		}
		WriteErrorWithCause(w, r, ErrPodCreateFailed, fmt.Sprintf("Failed to ensure pod for session %s", sessionId), err)
		return
	}

	// Get pod to read expiresAt
	pod, err := h.k8sClient.GetPod(ctx, result.PodName)
	if err != nil {
		WriteErrorWithCause(w, r, ErrPodGetFailed, fmt.Sprintf("Failed to get pod %s", result.PodName), err)
		return
	}

	resp := CreateSandboxResponse{
		PodName:   result.PodName,
		ExpiresAt: k8s.GetExpiresAtFromPod(pod),
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	if err := json.NewEncoder(w).Encode(resp); err != nil {
		// Log error but response already sent
		log.Printf("[ERROR] Failed to encode JSON response for CreateSandbox: %v", err)
	}

	h.metrics.RecordSandboxCreate()
}

// HandleTouch handles POST /v1/sandboxes/{sessionId}/touch
func (h *Handlers) HandleTouch(w http.ResponseWriter, r *http.Request) {
	userCtx, ok := auth.GetUserContext(r)
	if !ok {
		WriteError(w, r, ErrUnauthorized, "Unauthorized")
		return
	}

	sessionId := extractSessionId(r.URL.Path)
	if sessionId == "" {
		WriteError(w, r, ErrBadRequest, "sessionId required")
		return
	}

	// Verify access
	if err := h.authorizer.VerifySessionAccess(r.Context(), userCtx, sessionId); err != nil {
		WriteError(w, r, ErrForbidden, err.Error())
		return
	}

	ctx := r.Context()
	podName := k8s.PodName(sessionId)

	// Ensure pod exists and is ready (with auto-creation)
	pod, err := h.k8sClient.GetPod(ctx, podName)
	if err != nil {
		// Auto-create if not exists
		if ensureErr := h.ensurePodReady(ctx, sessionId, podName); ensureErr != nil {
			h.metrics.RecordK8sAPIFailure("EnsurePod")
			if isContextCanceled(ensureErr) {
				log.Printf("[DEBUG] Touch canceled for session %s: %v", sessionId, ensureErr)
			} else {
				log.Printf("[ERROR] Failed to ensure pod for session %s: %v", sessionId, ensureErr)
			}
			WriteErrorWithCause(w, r, ErrPodCreateFailed, fmt.Sprintf("Failed to ensure pod for session %s", sessionId), ensureErr)
			return
		}
		pod, err = h.k8sClient.GetPod(ctx, podName)
		if err != nil {
			WriteErrorWithCause(w, r, ErrPodGetFailed, fmt.Sprintf("Failed to get pod %s", podName), err)
			return
		}
	}

	ttl := k8s.GetTTLFromPod(pod)
	if ttl == 0 {
		ttl = h.cfg.Sandbox.Defaults.TTLSeconds
	}

	if err := h.k8sClient.PatchActivity(ctx, podName, ttl); err != nil {
		h.metrics.RecordK8sAPIFailure("PatchActivity")
		WriteErrorWithCause(w, r, ErrPodPatchFailed, fmt.Sprintf("Failed to patch activity for pod %s", podName), err)
		return
	}

	w.WriteHeader(http.StatusOK)
	h.metrics.RecordSandboxTouch()
}

// HandleExec handles POST /v1/sandboxes/{sessionId}/exec
func (h *Handlers) HandleExec(w http.ResponseWriter, r *http.Request) {
	userCtx, ok := auth.GetUserContext(r)
	if !ok {
		WriteError(w, r, ErrUnauthorized, "Unauthorized")
		return
	}

	sessionId := extractSessionId(r.URL.Path)
	if sessionId == "" {
		WriteError(w, r, ErrBadRequest, "sessionId required")
		return
	}

	// Verify access
	if err := h.authorizer.VerifySessionAccess(r.Context(), userCtx, sessionId); err != nil {
		WriteError(w, r, ErrForbidden, err.Error())
		return
	}

	var req ExecRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		WriteErrorWithCause(w, r, ErrBadRequest, "Invalid request body", err)
		return
	}
	defer r.Body.Close()

	// Validate command
	if len(req.Cmd) == 0 {
		WriteError(w, r, ErrBadRequest, "cmd is required")
		return
	}

	// Validate environment keys
	for key := range req.Env {
		if !h.cfg.ValidateEnvKey(key) {
			WriteError(w, r, ErrInvalidEnvKey, fmt.Sprintf("Invalid environment variable key: %s", key))
			return
		}
	}

	// Validate workdir
	workdir := req.Workdir
	if workdir == "" {
		workdir = h.cfg.Sandbox.Defaults.Workdir
	} else if !h.cfg.ValidateWorkdir(workdir) {
		WriteError(w, r, ErrInvalidWorkdir, fmt.Sprintf("Invalid workdir: %s", workdir))
		return
	}

	ctx := r.Context()
	podName := k8s.PodName(sessionId)

	// Ensure pod exists and is ready (with auto-creation)
	if err := h.ensurePodReady(ctx, sessionId, podName); err != nil {
		h.metrics.RecordK8sAPIFailure("EnsurePod")
		if isContextCanceled(err) {
			log.Printf("[DEBUG] Exec canceled for session %s: %v", sessionId, err)
		} else {
			log.Printf("[ERROR] Failed to ensure pod for Exec session %s: %v", sessionId, err)
		}
		WriteErrorWithCause(w, r, ErrPodNotReady, "Pod not ready", err)
		return
	}

	pod, err := h.k8sClient.GetPod(ctx, podName)
	if err != nil {
		WriteErrorWithCause(w, r, ErrPodGetFailed, "Failed to get pod", err)
		return
	}

	// Build timeout
	timeout := h.cfg.Exec.DefaultTimeout
	if req.TimeoutSeconds > 0 {
		timeout = time.Duration(req.TimeoutSeconds) * time.Second
		if timeout > h.cfg.Exec.MaxTimeout {
			WriteError(w, r, ErrExecTimeout, fmt.Sprintf("Timeout exceeds maximum of %v", h.cfg.Exec.MaxTimeout))
			return
		}
	}

	// Build command with wrapper
	wrapper := exec.NewWrapper(
		h.cfg.Exec.Shell.Bin,
		h.cfg.Exec.Shell.Args,
		h.cfg.Exec.ExitCodeMarker.Key,
		h.cfg.Exec.ExitCodeMarker.Stream,
	)

	cmdArgs := wrapper.GetCommandArgs(req.Env, workdir, req.Cmd)

	// Create output buffers
	stdoutBuf := exec.NewTailBufferWriter(h.cfg.Exec.StdoutMaxBytes, h.cfg.Exec.PreserveTailBytes)
	stderrBuf := exec.NewTailBufferWriter(h.cfg.Exec.StderrMaxBytes, h.cfg.Exec.PreserveTailBytes)

	startTime := time.Now()

	// Execute
	execOpts := &k8s.ExecOptions{
		Command:   cmdArgs,
		Stdout:    stdoutBuf,
		Stderr:    stderrBuf,
		TTY:       false,
		Timeout:   timeout,
		Container: h.cfg.Sandbox.Defaults.ContainerName,
	}

	result, err := h.k8sExecutor.ExecWithExitCode(ctx, podName, execOpts, h.cfg.Exec.ExitCodeMarker.Key)
	if err != nil && result.TimedOut {
		WriteError(w, r, ErrExecTimeout, fmt.Sprintf("Command timed out after %v for session %s", timeout, sessionId))
		return
	}
	if err != nil && !result.TimedOut {
		// Log non-timeout exec errors but still return the result (which may have partial output)
		log.Printf("[WARN] Exec command failed for session %s: %v", sessionId, err)
	}

	duration := time.Since(startTime)

	// Update activity
	ttl := k8s.GetTTLFromPod(pod)
	if ttl == 0 {
		ttl = h.cfg.Sandbox.Defaults.TTLSeconds
	}
	if err := h.k8sClient.PatchActivity(ctx, podName, ttl); err != nil {
		// Log the error but don't fail the request - the exec already succeeded
		log.Printf("[WARN] Failed to patch activity for pod %s after exec: %v", podName, err)
	}

	resp := ExecResponse{
		ExitCode:   result.ExitCode,
		Stdout:     result.Stdout,
		Stderr:     result.Stderr,
		DurationMs: duration.Milliseconds(),
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	if err := json.NewEncoder(w).Encode(resp); err != nil {
		// Log error but response already sent
		log.Printf("[ERROR] Failed to encode JSON response for Exec: %v", err)
	}

	h.metrics.RecordSandboxExec()
}

// HandleUpload handles POST /v1/sandboxes/{sessionId}/files/upload
func (h *Handlers) HandleUpload(w http.ResponseWriter, r *http.Request) {
	userCtx, ok := auth.GetUserContext(r)
	if !ok {
		WriteError(w, r, ErrUnauthorized, "Unauthorized")
		return
	}

	sessionId := extractSessionId(r.URL.Path)
	if sessionId == "" {
		WriteError(w, r, ErrBadRequest, "sessionId required")
		return
	}

	// Verify access
	if err := h.authorizer.VerifySessionAccess(r.Context(), userCtx, sessionId); err != nil {
		WriteError(w, r, ErrForbidden, err.Error())
		return
	}

	dest := r.URL.Query().Get("dest")
	if dest == "" {
		dest = h.cfg.Files.Upload.DefaultDest
	}

	// Validate destination path
	if !h.cfg.ValidateFilePath(dest) {
		WriteError(w, r, ErrInvalidPath, fmt.Sprintf("Invalid destination path: %s", dest))
		return
	}

	ctx := r.Context()
	podName := k8s.PodName(sessionId)

	// Ensure pod exists and is ready
	if err := h.ensurePodReady(ctx, sessionId, podName); err != nil {
		if isContextCanceled(err) {
			log.Printf("[DEBUG] Upload canceled for session %s: %v", sessionId, err)
		} else {
			log.Printf("[ERROR] Failed to ensure pod for Upload session %s: %v", sessionId, err)
		}
		WriteErrorWithCause(w, r, ErrPodNotReady, "Pod not ready", err)
		return
	}

	// Validate content type
	contentType := r.Header.Get("Content-Type")
	if contentType == "" {
		WriteError(w, r, ErrBadRequest, "Content-Type header is required")
		return
	}
	if !validation.ValidateUploadContentType(contentType) {
		WriteError(w, r, ErrUnsupportedMediaType, fmt.Sprintf("Unsupported content type: %s. Supported types: gzip, tar", contentType))
		return
	}

	// Check content length
	if r.ContentLength > 0 && r.ContentLength > h.cfg.Files.Upload.MaxBytes {
		WriteError(w, r, ErrUploadTooLarge, fmt.Sprintf("Upload exceeds maximum size of %d bytes", h.cfg.Files.Upload.MaxBytes))
		return
	}

	// Create uploader
	uploader := files.NewUploader(h.k8sExecutor, &files.UploadConfig{
		RootPrefix:     h.cfg.Files.RootPrefix,
		DefaultDest:    h.cfg.Files.Upload.DefaultDest,
		MaxBytes:       h.cfg.Files.Upload.MaxBytes,
		TarBin:         h.cfg.Files.Tar.Bin,
		RejectSymlinks: h.cfg.Files.Tar.RejectSymlinks,
	})

	// Limit reader
	limitedReader := io.LimitReader(r.Body, h.cfg.Files.Upload.MaxBytes)

	// Upload
	if err := uploader.Upload(ctx, podName, dest, limitedReader); err != nil {
		if files.IsUploadValidationError(err) {
			WriteError(w, r, ErrUploadValidationFailed, err.Error())
			return
		}
		WriteErrorWithCause(w, r, ErrUploadExecFailed, fmt.Sprintf("Upload failed for session %s", sessionId), err)
		return
	}

	w.WriteHeader(http.StatusOK)
	h.metrics.RecordSandboxUpload()
}

// HandleDownload handles GET /v1/sandboxes/{sessionId}/files/download
func (h *Handlers) HandleDownload(w http.ResponseWriter, r *http.Request) {
	userCtx, ok := auth.GetUserContext(r)
	if !ok {
		WriteError(w, r, ErrUnauthorized, "Unauthorized")
		return
	}

	sessionId := extractSessionId(r.URL.Path)
	if sessionId == "" {
		WriteError(w, r, ErrBadRequest, "sessionId required")
		return
	}

	// Verify access
	if err := h.authorizer.VerifySessionAccess(r.Context(), userCtx, sessionId); err != nil {
		WriteError(w, r, ErrForbidden, err.Error())
		return
	}

	src := r.URL.Query().Get("src")
	if src == "" {
		src = h.cfg.Files.Download.DefaultSrc
	}

	// Validate source path
	if !h.cfg.ValidateFilePath(src) {
		WriteError(w, r, ErrInvalidPath, fmt.Sprintf("Invalid source path: %s", src))
		return
	}

	ctx := r.Context()
	podName := k8s.PodName(sessionId)

	// Ensure pod exists and is ready
	pod, err := h.k8sClient.GetPod(ctx, podName)
	if err != nil {
		// Auto-create if not exists
		if ensureErr := h.ensurePodReady(ctx, sessionId, podName); ensureErr != nil {
			if isContextCanceled(ensureErr) {
				log.Printf("[DEBUG] Download canceled for session %s: %v", sessionId, ensureErr)
			} else {
				log.Printf("[ERROR] Failed to ensure pod for Download session %s: %v", sessionId, ensureErr)
			}
			WriteErrorWithCause(w, r, ErrPodNotFound, "Pod not ready", ensureErr)
			return
		}
		pod, err = h.k8sClient.GetPod(ctx, podName)
		if err != nil {
			WriteErrorWithCause(w, r, ErrPodGetFailed, "Failed to get pod", err)
			return
		}
	}

	if !k8s.IsPodReady(pod) {
		WriteError(w, r, ErrPodNotReady, "Pod not ready")
		return
	}

	// Create downloader
	downloader := files.NewDownloader(h.k8sExecutor, &files.DownloadConfig{
		RootPrefix: h.cfg.Files.RootPrefix,
		DefaultSrc: h.cfg.Files.Download.DefaultSrc,
		TarBin:     h.cfg.Files.Tar.Bin,
	})

	// Download
	stream, err := downloader.Download(ctx, podName, src)
	if err != nil {
		WriteErrorWithCause(w, r, ErrDownloadExecFailed, fmt.Sprintf("Download failed for session %s", sessionId), err)
		return
	}
	defer func() {
		if closeErr := stream.Close(); closeErr != nil {
			log.Printf("[WARN] Failed to close download stream for session %s: %v", sessionId, closeErr)
		}
	}()

	// Set headers
	w.Header().Set("Content-Type", "application/x-gzip")
	w.Header().Set("Content-Disposition", "attachment; filename=\"files.tar.gz\"")

	// Stream to client
	w.WriteHeader(http.StatusOK)
	if _, err := io.Copy(w, stream); err != nil {
		// Log streaming errors but we've already sent the status code
		log.Printf("[WARN] Error streaming download for session %s: %v", sessionId, err)
		return
	}

	// Update activity
	ttl := k8s.GetTTLFromPod(pod)
	if ttl == 0 {
		ttl = h.cfg.Sandbox.Defaults.TTLSeconds
	}
	if err := h.k8sClient.PatchActivity(ctx, podName, ttl); err != nil {
		// Log the error but don't fail the request - the download already succeeded
		log.Printf("[WARN] Failed to patch activity for pod %s after download: %v", podName, err)
	}

	h.metrics.RecordSandboxDownload()
}

// HandleDelete handles DELETE /v1/sandboxes/{sessionId}
func (h *Handlers) HandleDelete(w http.ResponseWriter, r *http.Request) {
	userCtx, ok := auth.GetUserContext(r)
	if !ok {
		WriteError(w, r, ErrUnauthorized, "Unauthorized")
		return
	}

	sessionId := extractSessionId(r.URL.Path)
	if sessionId == "" {
		WriteError(w, r, ErrBadRequest, "sessionId required")
		return
	}

	// Verify access
	if err := h.authorizer.VerifySessionAccess(r.Context(), userCtx, sessionId); err != nil {
		WriteError(w, r, ErrForbidden, err.Error())
		return
	}

	ctx := r.Context()
	podName := k8s.PodName(sessionId)

	if err := h.k8sClient.DeletePod(ctx, podName, 0); err != nil {
		if isContextCanceled(err) {
			log.Printf("[DEBUG] Delete canceled for session %s: %v", sessionId, err)
		} else {
			log.Printf("[ERROR] Failed to delete pod %s for session %s: %v", podName, sessionId, err)
		}
		WriteErrorWithCause(w, r, ErrPodDeleteFailed, fmt.Sprintf("Failed to delete pod %s for session %s", podName, sessionId), err)
		return
	}

	w.WriteHeader(http.StatusNoContent)
	h.metrics.RecordSandboxDelete()
}

// buildPodSpec builds a PodSpec from the request and config
func (h *Handlers) buildPodSpec(sessionId string, req *CreateSandboxRequest) *k8s.PodSpec {
	// Use defaults if not specified
	ttl := req.TTLSeconds
	if ttl == 0 {
		ttl = h.cfg.Sandbox.Defaults.TTLSeconds
	}

	image := req.Image
	if image == "" {
		image = h.cfg.Sandbox.Defaults.RunnerImage
	}

	cpuLimit := req.CPULimit
	if cpuLimit == "" {
		cpuLimit = h.cfg.Sandbox.Defaults.Resources.Limits.CPU
	}

	memLimit := req.MemoryLimit
	if memLimit == "" {
		memLimit = h.cfg.Sandbox.Defaults.Resources.Limits.Memory
	}

	ephemLimit := req.EphemeralStorageLimit
	if ephemLimit == "" {
		ephemLimit = h.cfg.Sandbox.Defaults.Resources.Limits.EphemeralStorage
	}

	// Use custom container name if provided
	containerName := req.ContainerName
	if containerName == "" {
		containerName = h.cfg.Sandbox.Defaults.ContainerName
	}

	// Use custom workdir if provided
	workdir := req.Workdir
	if workdir == "" {
		workdir = h.cfg.Sandbox.Defaults.Workdir
	}

	// Build volumes list
	volumes := make([]k8s.VolumeSpec, 0, len(h.cfg.Sandbox.Defaults.Volumes))
	for _, v := range h.cfg.Sandbox.Defaults.Volumes {
		volumes = append(volumes, k8s.VolumeSpec{
			Name:      v.Name,
			MountPath: v.MountPath,
			SizeLimit: v.SizeLimit,
		})
	}

	// Merge custom labels
	labels := make(map[string]string)
	for k, v := range h.cfg.Sandbox.Defaults.Labels {
		labels[k] = v
	}

	// Merge custom annotations
	annotations := make(map[string]string)
	for k, v := range h.cfg.Sandbox.Defaults.Annotations {
		annotations[k] = v
	}

	return &k8s.PodSpec{
		SessionID:             sessionId,
		Image:                 image,
		ImagePullPolicy:       h.cfg.Sandbox.Defaults.ImagePullPolicy,
		ImagePullSecrets:      h.cfg.Sandbox.Defaults.ImagePullSecrets,
		TTLSeconds:            ttl,
		CPULimit:              cpuLimit,
		MemoryLimit:           memLimit,
		EphemeralStorageLimit: ephemLimit,
		ContainerName:         containerName,
		Workdir:               workdir,
		ShellType:             "bash", // Enable shell-bridge
		Env:                   req.Env,
		ResourceRequests: k8s.ResourceRequests{
			CPU:    h.cfg.Sandbox.Defaults.Resources.Requests.CPU,
			Memory: h.cfg.Sandbox.Defaults.Resources.Requests.Memory,
		},
		ResourceLimits: k8s.ResourceLimits{
			CPU:              cpuLimit,
			Memory:           memLimit,
			EphemeralStorage: ephemLimit,
		},
		Labels:      labels,
		Annotations: annotations,
		Volumes:     volumes,
		SecurityContext: &k8s.PodSecurityConfig{
			NonRoot:             true,
			RunAsUser:           10001,
			DropAllCapabilities: true,
			ReadOnlyRoot:        false,
		},
	}
}

// ensurePodReady ensures a pod exists and is ready
func (h *Handlers) ensurePodReady(ctx context.Context, sessionId, podName string) error {
	pod, err := h.k8sClient.GetPod(ctx, podName)
	if err != nil {
		// Auto-create if not exists
		req := &CreateSandboxRequest{}
		podSpec := h.buildPodSpec(sessionId, req)

		result, err := h.k8sClient.EnsurePod(
			ctx,
			podSpec,
			h.cfg.Sandbox.Defaults.PodReadyWait,
			h.cfg.Sandbox.Defaults.PodPollInterval,
		)
		if err != nil {
			return err
		}

		pod, err = h.k8sClient.GetPod(ctx, result.PodName)
		if err != nil {
			return err
		}
	}

	if !k8s.IsPodReady(pod) {
		return fmt.Errorf("pod %s is not ready", podName)
	}

	return nil
}

// extractSessionId extracts sessionId from the URL path
func extractSessionId(path string) string {
	// Path format: /v1/sandboxes/{sessionId}/...
	parts := strings.Split(path, "/")
	if len(parts) >= 4 && parts[1] == "v1" && parts[2] == "sandboxes" {
		return parts[3]
	}
	return ""
}

// LogRequest logs HTTP request details
func LogRequest(r *http.Request, requestId string) {
	log.Printf("[%s] %s %s from %s", requestId, r.Method, r.URL.Path, r.RemoteAddr)
}

// isContextCanceled checks if an error is due to context cancellation
func isContextCanceled(err error) bool {
	if err == nil {
		return false
	}
	// Check for context.Canceled
	if err == context.Canceled || err == context.DeadlineExceeded {
		return true
	}
	// Check if error message contains context cancellation indicators
	errMsg := err.Error()
	return contains(errMsg, "context canceled") ||
		contains(errMsg, "operation was canceled") ||
		contains(errMsg, "deadline exceeded")
}

// contains checks if a string contains a substring (case-insensitive for error matching)
func contains(s, substr string) bool {
	return len(s) >= len(substr) && (s == substr ||
		len(s) > len(substr) && (
			s[:len(substr)] == substr ||
			s[len(s)-len(substr):] == substr ||
			containsMiddle(s, substr)))
}

// containsMiddle checks if substr is in the middle of s
func containsMiddle(s, substr string) bool {
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}
