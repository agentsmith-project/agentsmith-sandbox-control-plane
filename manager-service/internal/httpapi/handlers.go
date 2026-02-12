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

	"k8s.io/apimachinery/pkg/api/resource"

	"github.com/sandbox/manager/internal/config"
	"github.com/sandbox/manager/internal/exec"
	"github.com/sandbox/manager/internal/files"
	"github.com/sandbox/manager/internal/k8s"
	"github.com/sandbox/manager/internal/observability"
	"github.com/sandbox/manager/internal/storage"
)

// maxJSONBodySize is the maximum allowed size for JSON request bodies (1 MB).
// This prevents trivial OOM DoS attacks via oversized request bodies.
const maxJSONBodySize = 1 * 1024 * 1024

// Manager is the interface for handlers to interact with the service
type Manager interface {
	GetConfig() *config.Config
	GetK8sClient() *k8s.Client
	GetK8sExecutor() *k8s.Executor
	GetMetrics() *observability.MetricsRegistry
	GetStorageClient() *storage.Client
}

// Handlers contains all the HTTP handlers.
// Config is always read live via mgr.GetConfig() so that hot-reloaded
// values take effect without restarting the process.
type Handlers struct {
	mgr           Manager
	k8sClient     *k8s.Client
	k8sExecutor   *k8s.Executor
	metrics       *observability.MetricsRegistry
	storageClient *storage.Client
}

// NewHandlers creates a new handlers instance
func NewHandlers(mgr Manager) *Handlers {
	return &Handlers{
		mgr:           mgr,
		k8sClient:     mgr.GetK8sClient(),
		k8sExecutor:   mgr.GetK8sExecutor(),
		metrics:       mgr.GetMetrics(),
		storageClient: mgr.GetStorageClient(),
	}
}

// config returns the current (possibly hot-reloaded) configuration.
func (h *Handlers) config() *config.Config {
	return h.mgr.GetConfig()
}

// HandleCreateSandbox handles PUT /v1/sandboxes/{sessionId}
func (h *Handlers) HandleCreateSandbox(w http.ResponseWriter, r *http.Request) {
	sessionId := extractSessionId(r.URL.Path)
	if sessionId == "" {
		WriteError(w, r, ErrBadRequest, "sessionId required")
		return
	}

	r.Body = http.MaxBytesReader(w, r.Body, maxJSONBodySize)
	var req CreateSandboxRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		WriteErrorWithCause(w, r, ErrBadRequest, "Invalid request body", err)
		return
	}
	defer r.Body.Close()

	// Validate environment variable keys
	for key := range req.Env {
		if !h.config().ValidateEnvKey(key) {
			WriteError(w, r, ErrInvalidEnvKey, fmt.Sprintf("Invalid environment variable key: %s", key))
			return
		}
	}

	// Validate workdir if provided
	if req.Workdir != "" && !h.config().ValidateWorkdir(req.Workdir) {
		WriteError(w, r, ErrInvalidWorkdir, fmt.Sprintf("Invalid workdir: %s", req.Workdir))
		return
	}

	ctx := r.Context()
	podSpec := h.buildPodSpec(sessionId, &req)

	result, err := h.k8sClient.EnsurePod(
		ctx,
		podSpec,
		h.config().Sandbox.Defaults.PodReadyWait,
		h.config().Sandbox.Defaults.PodPollInterval,
	)
	if err != nil {
		h.metrics.RecordK8sAPIFailure("EnsurePod")
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
	json.NewEncoder(w).Encode(resp)

	h.metrics.RecordSandboxCreate()
}

// HandleTouch handles POST /v1/sandboxes/{sessionId}/touch
func (h *Handlers) HandleTouch(w http.ResponseWriter, r *http.Request) {
	sessionId := extractSessionId(r.URL.Path)
	if sessionId == "" {
		WriteError(w, r, ErrBadRequest, "sessionId required")
		return
	}

	ctx := r.Context()
	podName := k8s.PodName(sessionId)

	pod, err := h.k8sClient.GetPod(ctx, podName)
	if err != nil {
		WriteErrorWithCause(w, r, ErrPodNotFound, fmt.Sprintf("Pod not found for session %s", sessionId), err)
		return
	}

	ttl := k8s.GetTTLFromPod(pod)
	if ttl == 0 {
		ttl = h.config().Sandbox.Defaults.TTLSeconds
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
// Returns an SSE stream with stdout, stderr, and exit events.
func (h *Handlers) HandleExec(w http.ResponseWriter, r *http.Request) {
	sessionId := extractSessionId(r.URL.Path)
	if sessionId == "" {
		WriteError(w, r, ErrBadRequest, "sessionId required")
		return
	}

	r.Body = http.MaxBytesReader(w, r.Body, maxJSONBodySize)
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
		if !h.config().ValidateEnvKey(key) {
			WriteError(w, r, ErrInvalidEnvKey, fmt.Sprintf("Invalid environment variable key: %s", key))
			return
		}
	}

	// Validate workdir
	workdir := req.Workdir
	if workdir == "" {
		workdir = h.config().Sandbox.Defaults.Workdir
	} else if !h.config().ValidateWorkdir(workdir) {
		WriteError(w, r, ErrInvalidWorkdir, fmt.Sprintf("Invalid workdir: %s", workdir))
		return
	}

	ctx := r.Context()
	podName := k8s.PodName(sessionId)

	// Ensure pod exists and is ready
	if err := h.ensurePodReady(ctx, sessionId, podName); err != nil {
		WriteErrorWithCause(w, r, ErrPodNotReady, "Pod not ready", err)
		return
	}

	pod, err := h.k8sClient.GetPod(ctx, podName)
	if err != nil {
		WriteErrorWithCause(w, r, ErrPodGetFailed, "Failed to get pod", err)
		return
	}

	// Build timeout
	timeout := h.config().Exec.DefaultTimeout
	if req.TimeoutSeconds > 0 {
		timeout = time.Duration(req.TimeoutSeconds) * time.Second
		if timeout > h.config().Exec.MaxTimeout {
			WriteError(w, r, ErrExecTimeout, fmt.Sprintf("Timeout exceeds maximum of %v", h.config().Exec.MaxTimeout))
			return
		}
	}

	// Set up SSE response
	sse := NewSSEWriter(w)
	if sse == nil {
		WriteError(w, r, ErrExecFailed, "Streaming not supported")
		return
	}
	sse.WriteHeaders()

	// Build command with wrapper
	wrapper := exec.NewWrapper(
		h.config().Exec.Shell.Bin,
		h.config().Exec.Shell.Args,
		h.config().Exec.ExitCodeMarker.Key,
		h.config().Exec.ExitCodeMarker.Stream,
	)
	cmdArgs := wrapper.GetCommandArgs(req.Env, workdir, req.Cmd)

	startTime := time.Now()

	// Execute with streaming output via SSE writers
	execOpts := &k8s.ExecOptions{
		Command:   cmdArgs,
		Stdout:    sse.StdoutWriter(),
		Stderr:    sse.StderrWriter(),
		TTY:       false,
		Timeout:   timeout,
		Container: h.config().Sandbox.Defaults.ContainerName,
	}

	result, execErr := h.k8sExecutor.ExecWithExitCode(ctx, podName, execOpts, h.config().Exec.ExitCodeMarker.Key)

	duration := time.Since(startTime)

	// Send exit event
	exitCode := 0
	if result != nil {
		exitCode = result.ExitCode
	}
	if execErr != nil {
		if result != nil && result.TimedOut {
			sse.WriteEvent("error", SSEErrorEvent{Message: fmt.Sprintf("Command timed out after %v", timeout)})
			exitCode = 124 // Standard timeout exit code
		} else {
			// Non-timeout exec failure (SPDY error, container crash, etc.).
			// Send an error event so the client knows what happened.
			sse.WriteEvent("error", SSEErrorEvent{Message: fmt.Sprintf("Exec failed: %v", execErr)})
			if exitCode == 0 {
				exitCode = -1 // Signal abnormal termination
			}
		}
	}

	sse.WriteEvent("exit", SSEExitEvent{
		ExitCode:   exitCode,
		DurationMs: duration.Milliseconds(),
	})

	// Update activity
	ttl := k8s.GetTTLFromPod(pod)
	if ttl == 0 {
		ttl = h.config().Sandbox.Defaults.TTLSeconds
	}
	if err := h.k8sClient.PatchActivity(ctx, podName, ttl); err != nil {
		log.Printf("[WARN] Failed to patch activity for pod %s after exec: %v", podName, err)
	}

	h.metrics.RecordSandboxExec()
}

// HandleUpload handles POST /v1/sandboxes/{sessionId}/files/upload
func (h *Handlers) HandleUpload(w http.ResponseWriter, r *http.Request) {
	sessionId := extractSessionId(r.URL.Path)
	if sessionId == "" {
		WriteError(w, r, ErrBadRequest, "sessionId required")
		return
	}

	defer r.Body.Close()

	dest := r.URL.Query().Get("dest")
	if dest == "" {
		dest = h.config().Files.Upload.DefaultDest
	}

	if !h.config().ValidateFilePath(dest) {
		WriteError(w, r, ErrInvalidPath, fmt.Sprintf("Invalid destination path: %s", dest))
		return
	}

	ctx := r.Context()
	podName := k8s.PodName(sessionId)

	if err := h.ensurePodReady(ctx, sessionId, podName); err != nil {
		WriteErrorWithCause(w, r, ErrPodNotReady, "Pod not ready", err)
		return
	}

	if r.ContentLength > 0 && r.ContentLength > h.config().Files.Upload.MaxBytes {
		WriteError(w, r, ErrUploadTooLarge, fmt.Sprintf("Upload exceeds maximum size of %d bytes", h.config().Files.Upload.MaxBytes))
		return
	}

	uploader := files.NewUploader(h.k8sExecutor, &files.UploadConfig{
		RootPrefix:     h.config().Files.RootPrefix,
		DefaultDest:    h.config().Files.Upload.DefaultDest,
		MaxBytes:       h.config().Files.Upload.MaxBytes,
		TarBin:         h.config().Files.Tar.Bin,
		RejectSymlinks: h.config().Files.Tar.RejectSymlinks,
	})

	limitedReader := io.LimitReader(r.Body, h.config().Files.Upload.MaxBytes)

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
	sessionId := extractSessionId(r.URL.Path)
	if sessionId == "" {
		WriteError(w, r, ErrBadRequest, "sessionId required")
		return
	}

	src := r.URL.Query().Get("src")
	if src == "" {
		src = h.config().Files.Download.DefaultSrc
	}

	if !h.config().ValidateFilePath(src) {
		WriteError(w, r, ErrInvalidPath, fmt.Sprintf("Invalid source path: %s", src))
		return
	}

	ctx := r.Context()
	podName := k8s.PodName(sessionId)

	pod, err := h.k8sClient.GetPod(ctx, podName)
	if err != nil {
		WriteErrorWithCause(w, r, ErrPodNotFound, "Pod not found", err)
		return
	}

	if !k8s.IsPodReady(pod) {
		WriteError(w, r, ErrPodNotReady, "Pod not ready")
		return
	}

	downloader := files.NewDownloader(h.k8sExecutor, &files.DownloadConfig{
		RootPrefix: h.config().Files.RootPrefix,
		DefaultSrc: h.config().Files.Download.DefaultSrc,
		TarBin:     h.config().Files.Tar.Bin,
	})

	stream, err := downloader.Download(ctx, podName, src)
	if err != nil {
		WriteErrorWithCause(w, r, ErrDownloadExecFailed, fmt.Sprintf("Download failed for session %s", sessionId), err)
		return
	}
	defer stream.Close()

	w.Header().Set("Content-Type", "application/x-gzip")
	w.Header().Set("Content-Disposition", "attachment; filename=\"files.tar.gz\"")
	w.WriteHeader(http.StatusOK)

	if _, err := io.Copy(w, stream); err != nil {
		log.Printf("[WARN] Error streaming download for session %s: %v", sessionId, err)
		return
	}

	// Update activity
	ttl := k8s.GetTTLFromPod(pod)
	if ttl == 0 {
		ttl = h.config().Sandbox.Defaults.TTLSeconds
	}
	if err := h.k8sClient.PatchActivity(ctx, podName, ttl); err != nil {
		log.Printf("[WARN] Failed to patch activity for pod %s after download: %v", podName, err)
	}

	h.metrics.RecordSandboxDownload()
}

// HandleDelete handles DELETE /v1/sandboxes/{sessionId}
//
// The handler performs a synchronous snapshot of the workspace BEFORE
// issuing the pod deletion. This avoids the race condition where
// gracePeriodSeconds=0 causes the kubelet to terminate the container
// before the async finalizer handler can take a snapshot.
//
// After the snapshot (or if it fails/times out), the pod is deleted.
// The finalizer on the pod is a safety net for edge cases only.
func (h *Handlers) HandleDelete(w http.ResponseWriter, r *http.Request) {
	sessionId := extractSessionId(r.URL.Path)
	if sessionId == "" {
		WriteError(w, r, ErrBadRequest, "sessionId required")
		return
	}

	ctx := r.Context()
	podName := k8s.PodName(sessionId)

	// Best-effort synchronous snapshot before deletion.
	// We snapshot while the container is still running so the data is intact.
	pod, err := h.k8sClient.GetPod(ctx, podName)
	if err == nil && k8s.IsPodReady(pod) && h.storageClient != nil {
		snapshotCtx, snapshotCancel := context.WithTimeout(ctx, 2*time.Minute)
		if err := h.snapshotBeforeDelete(snapshotCtx, podName, sessionId); err != nil {
			log.Printf("[WARN] Pre-delete snapshot failed for pod %s: %v (proceeding with delete)", podName, err)
		}
		snapshotCancel()
	}

	if err := h.k8sClient.DeletePod(ctx, podName, 0); err != nil {
		WriteErrorWithCause(w, r, ErrPodDeleteFailed, fmt.Sprintf("Failed to delete pod %s", podName), err)
		return
	}

	w.WriteHeader(http.StatusNoContent)
	h.metrics.RecordSandboxDelete()
}

// snapshotBeforeDelete creates and uploads a workspace snapshot synchronously.
func (h *Handlers) snapshotBeforeDelete(ctx context.Context, podName, sessionId string) error {
	ns := h.config().Sandbox.Defaults.Namespace
	snapshot, err := h.k8sClient.SnapshotWorkspace(ctx, ns, podName)
	if err != nil {
		return fmt.Errorf("snapshot creation failed: %w", err)
	}
	defer snapshot.Close()

	key, err := h.storageClient.GenerateSnapshotKey(sessionId)
	if err != nil {
		return fmt.Errorf("snapshot key generation failed: %w", err)
	}

	if err := h.storageClient.UploadSnapshot(ctx, key, snapshot, -1); err != nil {
		return fmt.Errorf("snapshot upload failed: %w", err)
	}

	log.Printf("Pre-delete snapshot uploaded for pod %s (session=%s, key=%s)", podName, sessionId, key)
	return nil
}

// buildPodSpec builds a PodSpec from the request and config.
// Request TTL and resource limits are clamped to config maxima (config ttlSeconds and resources.limits).
func (h *Handlers) buildPodSpec(sessionId string, req *CreateSandboxRequest) *k8s.PodSpec {
	cfg := h.config().Sandbox.Defaults
	maxTTL := cfg.TTLSeconds
	ttl := req.TTLSeconds
	if ttl == 0 {
		ttl = maxTTL
	} else if ttl > maxTTL {
		ttl = maxTTL
	}

	image := req.Image
	if image == "" {
		image = cfg.RunnerImage
	}

	// Clamp resource limits to config maxima (config defines the max users can set)
	maxLimits := cfg.Resources.Limits
	cpuLimit := req.CPULimit
	if cpuLimit == "" {
		cpuLimit = maxLimits.CPU
	} else if maxLimits.CPU != "" && !resourceQuantityLessOrEqual(cpuLimit, maxLimits.CPU) {
		cpuLimit = maxLimits.CPU
	}

	memLimit := req.MemoryLimit
	if memLimit == "" {
		memLimit = maxLimits.Memory
	} else if maxLimits.Memory != "" && !resourceQuantityLessOrEqual(memLimit, maxLimits.Memory) {
		memLimit = maxLimits.Memory
	}

	ephemLimit := req.EphemeralStorageLimit
	if ephemLimit == "" {
		ephemLimit = maxLimits.EphemeralStorage
	} else if maxLimits.EphemeralStorage != "" && !resourceQuantityLessOrEqual(ephemLimit, maxLimits.EphemeralStorage) {
		ephemLimit = maxLimits.EphemeralStorage
	}

	containerName := req.ContainerName
	if containerName == "" {
		containerName = cfg.ContainerName
	}

	workdir := req.Workdir
	if workdir == "" {
		workdir = cfg.Workdir
	}

	// Build volumes
	volumes := make([]k8s.VolumeSpec, 0, len(cfg.Volumes))
	for _, v := range cfg.Volumes {
		volumes = append(volumes, k8s.VolumeSpec{
			Name:      v.Name,
			MountPath: v.MountPath,
			SizeLimit: v.SizeLimit,
		})
	}

	// Merge labels
	labels := make(map[string]string)
	for k, v := range cfg.Labels {
		labels[k] = v
	}

	// Merge annotations
	annotations := make(map[string]string)
	for k, v := range cfg.Annotations {
		annotations[k] = v
	}

	return &k8s.PodSpec{
		SessionID:             sessionId,
		Image:                 image,
		ImagePullPolicy:       cfg.ImagePullPolicy,
		ImagePullSecrets:      cfg.ImagePullSecrets,
		TTLSeconds:            ttl,
		CPULimit:              cpuLimit,
		MemoryLimit:           memLimit,
		EphemeralStorageLimit: ephemLimit,
		ContainerName:         containerName,
		Workdir:               workdir,
		Env:                   req.Env,
		ResourceRequests: k8s.ResourceRequests{
			CPU:    cfg.Resources.Requests.CPU,
			Memory: cfg.Resources.Requests.Memory,
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

// ensurePodReady verifies a pod exists and is ready.
// If the pod does not exist, an error is returned so the caller can
// instruct the client to re-create the sandbox explicitly with the
// correct parameters (image, env, resources, etc.).
func (h *Handlers) ensurePodReady(ctx context.Context, sessionId, podName string) error {
	pod, err := h.k8sClient.GetPod(ctx, podName)
	if err != nil {
		return fmt.Errorf("pod %s not found for session %s: %w", podName, sessionId, err)
	}

	if !k8s.IsPodReady(pod) {
		return fmt.Errorf("pod %s is not ready", podName)
	}

	return nil
}

// maxSessionIdLen is the maximum allowed length for a sessionId.
const maxSessionIdLen = 128

// extractSessionId extracts and validates the sessionId from the URL path.
// Returns an empty string if the path doesn't contain a valid sessionId.
//
// Valid sessionId: 1-128 characters, alphanumeric plus hyphens, underscores, and dots.
// Must start with an alphanumeric character. No path separators or traversal sequences.
func extractSessionId(path string) string {
	parts := strings.Split(path, "/")
	if len(parts) >= 4 && parts[1] == "v1" && parts[2] == "sandboxes" {
		id := parts[3]
		if isValidSessionId(id) {
			return id
		}
	}
	return ""
}

// isValidSessionId checks whether a sessionId is safe and well-formed.
func isValidSessionId(id string) bool {
	if len(id) == 0 || len(id) > maxSessionIdLen {
		return false
	}
	// Must start with alphanumeric
	if !isAlphaNum(id[0]) {
		return false
	}
	for i := 1; i < len(id); i++ {
		c := id[i]
		if !isAlphaNum(c) && c != '-' && c != '_' && c != '.' {
			return false
		}
	}
	return true
}

func isAlphaNum(c byte) bool {
	return (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') || (c >= '0' && c <= '9')
}

// resourceQuantityLessOrEqual returns true if a parses as a quantity <= b. Invalid or empty quantities return false so we clamp to max.
func resourceQuantityLessOrEqual(a, b string) bool {
	if a == "" || b == "" {
		return true
	}
	qtyA, errA := resource.ParseQuantity(a)
	qtyB, errB := resource.ParseQuantity(b)
	if errA != nil || errB != nil {
		return false
	}
	return qtyA.Cmp(qtyB) <= 0
}
