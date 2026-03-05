package workload

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	v1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	retryutil "github.com/sandbox/manager/internal/retry"
	"github.com/sandbox/manager/internal/k8s"
	"github.com/sandbox/manager/internal/observability"
)

var k8sRetryConfig = retryutil.RetryConfig{
	MaxAttempts:    3,
	InitialBackoff: 500 * time.Millisecond,
	MaxBackoff:     5 * time.Second,
	BackoffFactor:  2.0,
}

// Handler provides REST endpoints for managed workload pod lifecycle.
type Handler struct {
	k8sClient *k8s.Client
	executor  *k8s.Executor
	pvcName   string
	basePath  string // JuiceFS mount root for workspace directories
}

// NewHandler creates a new workload handler.
func NewHandler(k8sClient *k8s.Client, executor *k8s.Executor, pvcName string, basePath string) *Handler {
	return &Handler{
		k8sClient: k8sClient,
		executor:  executor,
		pvcName:   pvcName,
		basePath:  basePath,
	}
}

// RegisterRoutes registers workload HTTP routes on the given mux.
func (h *Handler) RegisterRoutes(mux *http.ServeMux) {
	mux.HandleFunc("/v1/workspaces/", h.routeRequest)
}

// parseRoute extracts workspaceID, projectID, workloadID, and action from the URL path.
// Path format: /v1/workspaces/{wsId}/projects/{projId}/workloads/{wlId}[/keepalive|/exec]
func parseRoute(path string) (workspaceID, projectID, workloadID, action string, ok bool) {
	trimmed := strings.TrimPrefix(path, "/v1/workspaces/")
	if trimmed == path {
		return
	}

	parts := strings.Split(trimmed, "/")
	if len(parts) < 5 {
		return
	}
	if parts[1] != "projects" || parts[3] != "workloads" {
		return
	}

	workspaceID = parts[0]
	projectID = parts[2]
	workloadID = parts[4]

	if len(parts) == 6 {
		action = parts[5]
	}

	ok = workspaceID != "" && projectID != "" && workloadID != ""
	return
}

func (h *Handler) routeRequest(w http.ResponseWriter, r *http.Request) {
	workspaceID, projectID, workloadID, action, ok := parseRoute(r.URL.Path)
	if !ok {
		http.Error(w, "not found", http.StatusNotFound)
		return
	}

	if !isValidK8sName(workloadID) {
		jsonError(w, http.StatusBadRequest, "invalid workload_id: must be lowercase alphanumeric or hyphens, max 63 chars")
		return
	}

	switch {
	case action == "" && r.Method == http.MethodPut:
		h.handleCreatePod(w, r, workspaceID, projectID, workloadID)
	case action == "" && r.Method == http.MethodDelete:
		h.handleDeletePod(w, r, workspaceID, projectID, workloadID)
	case action == "" && r.Method == http.MethodGet:
		h.handleGetPod(w, r, workloadID)
	case action == "keepalive" && r.Method == http.MethodPost:
		h.handleKeepalive(w, r, workloadID)
	case action == "exec" && r.Method == http.MethodPost:
		h.handleExec(w, r, workloadID)
	default:
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
	}
}

func (h *Handler) handleCreatePod(w http.ResponseWriter, r *http.Request, workspaceID, projectID, workloadID string) {
	var req CreateRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		jsonError(w, http.StatusBadRequest, "invalid request body: "+err.Error())
		return
	}

	if req.Image == "" {
		jsonError(w, http.StatusBadRequest, "image is required")
		return
	}

	ctx := r.Context()

	subPath := filepath.Join(workspaceID, workloadID)
	fullPath := filepath.Join(h.basePath, subPath)
	if err := os.MkdirAll(fullPath, 0755); err != nil {
		log.Printf("workload/%s: mkdir failed: %v", workloadID, err)
		jsonError(w, http.StatusInternalServerError, "workspace dir creation failed: "+err.Error())
		return
	}
	if err := os.Chown(fullPath, workloadRunAsUID, workloadRunAsUID); err != nil {
		log.Printf("workload/%s: chown warning (non-fatal): %v", workloadID, err)
	}
	log.Printf("workload/%s: workspace dir ready at %s", workloadID, fullPath)

	idleTimeout := DefaultIdleTimeout
	if req.IdleTimeoutSec > 0 {
		idleTimeout = time.Duration(req.IdleTimeoutSec) * time.Second
	}
	maxLifetime := DefaultMaxLifetime
	if req.MaxLifetimeSec > 0 {
		maxLifetime = time.Duration(req.MaxLifetimeSec) * time.Second
	}

	now := time.Now().UTC()
	expiresAt := now.Add(maxLifetime)
	idleExpiresAt := now.Add(idleTimeout)
	if idleExpiresAt.Before(expiresAt) {
		expiresAt = idleExpiresAt
	}

	podName := PodName(workloadID)

	env := make(map[string]string)
	for k, v := range req.Env {
		env[k] = v
	}
	env["WORKSPACE_PATH"] = "/workspace"

	command := DefaultKeepAliveCommand
	if len(req.Command) > 0 {
		command = req.Command
	}

	pod, err := h.buildPod(workspaceID, projectID, workloadID, podName, subPath, env, command, req, now, expiresAt)
	if err != nil {
		jsonError(w, http.StatusBadRequest, err.Error())
		return
	}

	createdPod, err := h.k8sClient.Clientset().CoreV1().Pods(h.k8sClient.Namespace()).Create(ctx, pod, metav1.CreateOptions{})
	if err != nil {
		if errors.IsAlreadyExists(err) {
			existingPod, getErr := h.k8sClient.GetPod(ctx, podName)
			if getErr == nil {
				jsonResponse(w, http.StatusOK, PodStatus{
					PodName:   existingPod.Name,
					Phase:     string(existingPod.Status.Phase),
					IP:        existingPod.Status.PodIP,
					StartedAt: existingPod.CreationTimestamp.Format(time.RFC3339),
					Message:   "pod already exists",
				})
				return
			}
		}
		log.Printf("workload/%s: pod creation failed: %v", workloadID, err)
		jsonError(w, http.StatusInternalServerError, "pod creation failed: "+err.Error())
		return
	}

	ready, err := h.k8sClient.WaitForPodReady(ctx, podName, 120*time.Second, 2*time.Second)
	if err != nil {
		log.Printf("workload/%s: pod not ready: %v", workloadID, err)
	}

	status := PodStatus{
		PodName:   createdPod.Name,
		Phase:     string(createdPod.Status.Phase),
		StartedAt: createdPod.CreationTimestamp.Format(time.RFC3339),
		ExpiresAt: expiresAt.Format(time.RFC3339),
	}
	if ready {
		refreshedPod, err := h.k8sClient.GetPod(ctx, podName)
		if err == nil {
			status.Phase = string(refreshedPod.Status.Phase)
			status.IP = refreshedPod.Status.PodIP
		}
	}

	observability.GetMetrics().RecordWorkloadCreate()
	jsonResponse(w, http.StatusCreated, status)
}

func (h *Handler) handleDeletePod(w http.ResponseWriter, r *http.Request, workspaceID, projectID, workloadID string) {
	ctx := r.Context()
	podName := PodName(workloadID)

	_, err := h.k8sClient.GetPod(ctx, podName)
	if err != nil {
		if errors.IsNotFound(err) {
			jsonError(w, http.StatusNotFound, "pod not found")
			return
		}
		jsonError(w, http.StatusInternalServerError, err.Error())
		return
	}

	if err := retryutil.Retry(ctx, k8sRetryConfig, func() error {
		return h.k8sClient.DeletePod(ctx, podName, 10)
	}); err != nil {
		log.Printf("workload/%s: pod deletion failed: %v", workloadID, err)
		jsonError(w, http.StatusInternalServerError, "pod deletion failed: "+err.Error())
		return
	}

	h.waitForPodDeletion(ctx, podName, 30*time.Second)

	observability.GetMetrics().RecordWorkloadDelete()
	jsonResponse(w, http.StatusOK, DeleteResponse{Message: "pod deleted"})
}

func (h *Handler) handleGetPod(w http.ResponseWriter, r *http.Request, workloadID string) {
	ctx := r.Context()
	podName := PodName(workloadID)

	pod, err := h.k8sClient.GetPod(ctx, podName)
	if err != nil {
		if errors.IsNotFound(err) {
			jsonResponse(w, http.StatusOK, PodStatus{Phase: "offline"})
			return
		}
		jsonError(w, http.StatusInternalServerError, err.Error())
		return
	}

	status := PodStatus{
		PodName:   pod.Name,
		Phase:     string(pod.Status.Phase),
		IP:        pod.Status.PodIP,
		StartedAt: pod.CreationTimestamp.Format(time.RFC3339),
	}
	if v, ok := pod.Annotations["last_activity_at"]; ok {
		status.LastActivityAt = v
	}
	if v, ok := pod.Annotations["expires_at"]; ok {
		status.ExpiresAt = v
	}

	jsonResponse(w, http.StatusOK, status)
}

// handleKeepalive updates the pod's last-activity and expires_at. Clients must send keepalive
// periodically; if no keepalive is received for idle_timeout_sec, the cleaner will reclaim the pod.
func (h *Handler) handleKeepalive(w http.ResponseWriter, r *http.Request, workloadID string) {
	ctx := r.Context()
	podName := PodName(workloadID)

	pod, err := h.k8sClient.GetPod(ctx, podName)
	if err != nil {
		if errors.IsNotFound(err) {
			jsonError(w, http.StatusNotFound, "pod not found")
			return
		}
		jsonError(w, http.StatusInternalServerError, err.Error())
		return
	}

	idleTimeout := DefaultIdleTimeout
	if v, ok := pod.Annotations["workload/idleTimeoutSec"]; ok {
		if sec, err := strconv.Atoi(v); err == nil {
			idleTimeout = time.Duration(sec) * time.Second
		}
	}

	now := time.Now().UTC()
	newExpires := now.Add(idleTimeout)

	if v, ok := pod.Annotations["workload/maxExpiresAt"]; ok {
		if maxT, err := time.Parse(time.RFC3339, v); err == nil && newExpires.After(maxT) {
			newExpires = maxT
		}
	}

	if err := retryutil.Retry(ctx, k8sRetryConfig, func() error {
		return h.k8sClient.PatchActivity(ctx, podName, newExpires)
	}); err != nil {
		jsonError(w, http.StatusInternalServerError, "failed to update keepalive: "+err.Error())
		return
	}

	observability.GetMetrics().RecordWorkloadKeepalive()
	jsonResponse(w, http.StatusOK, KeepaliveResponse{
		ExpiresAt: newExpires.Format(time.RFC3339),
	})
}

func (h *Handler) handleExec(w http.ResponseWriter, r *http.Request, workloadID string) {
	var req ExecRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		jsonError(w, http.StatusBadRequest, "invalid request body: "+err.Error())
		return
	}
	if len(req.Cmd) == 0 {
		jsonError(w, http.StatusBadRequest, "cmd is required")
		return
	}

	ctx := r.Context()
	podName := PodName(workloadID)

	exists, err := h.k8sClient.PodExists(ctx, podName)
	if err != nil {
		jsonError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if !exists {
		jsonError(w, http.StatusNotFound, "pod not found")
		return
	}

	timeout := 30 * time.Second
	if req.TimeoutSeconds > 0 {
		timeout = time.Duration(req.TimeoutSeconds) * time.Second
	}
	if timeout > maxExecTimeout {
		timeout = maxExecTimeout
	}

	result, err := h.executor.Exec(ctx, podName, &k8s.ExecOptions{
		Command:   req.Cmd,
		Timeout:   timeout,
		Container: "main",
	})

	if err != nil && result == nil {
		jsonError(w, http.StatusInternalServerError, "exec failed: "+err.Error())
		return
	}

	resp := ExecResponse{
		ExitCode:   result.ExitCode,
		Stdout:     result.Stdout,
		Stderr:     result.Stderr,
		DurationMs: result.Duration.Milliseconds(),
	}
	observability.GetMetrics().RecordWorkloadExec()
	jsonResponse(w, http.StatusOK, resp)
}

// parseResourceRequirements parses create request resource fields and returns
// v1.ResourceRequirements. Returns an error for invalid quantity strings.
func parseResourceRequirements(req CreateRequest) (v1.ResourceRequirements, error) {
	var out v1.ResourceRequirements
	if req.CPURequest != "" || req.MemoryRequest != "" {
		out.Requests = v1.ResourceList{}
		if req.CPURequest != "" {
			q, err := resource.ParseQuantity(req.CPURequest)
			if err != nil {
				return v1.ResourceRequirements{}, fmt.Errorf("invalid cpu_request: %w", err)
			}
			out.Requests[v1.ResourceCPU] = q
		}
		if req.MemoryRequest != "" {
			q, err := resource.ParseQuantity(req.MemoryRequest)
			if err != nil {
				return v1.ResourceRequirements{}, fmt.Errorf("invalid memory_request: %w", err)
			}
			out.Requests[v1.ResourceMemory] = q
		}
	}
	if req.CPULimit != "" || req.MemoryLimit != "" {
		out.Limits = v1.ResourceList{}
		if req.CPULimit != "" {
			q, err := resource.ParseQuantity(req.CPULimit)
			if err != nil {
				return v1.ResourceRequirements{}, fmt.Errorf("invalid cpu_limit: %w", err)
			}
			out.Limits[v1.ResourceCPU] = q
		}
		if req.MemoryLimit != "" {
			q, err := resource.ParseQuantity(req.MemoryLimit)
			if err != nil {
				return v1.ResourceRequirements{}, fmt.Errorf("invalid memory_limit: %w", err)
			}
			out.Limits[v1.ResourceMemory] = q
		}
	}
	return out, nil
}

func (h *Handler) buildPod(
	workspaceID, projectID, workloadID, podName, subPath string,
	env map[string]string,
	command []string,
	req CreateRequest,
	now time.Time,
	expiresAt time.Time,
) (*v1.Pod, error) {
	labels := map[string]string{
		"app":          WorkloadLabel,
		"workload_id":  workloadID,
		"workspace_id": workspaceID,
		"project_id":   projectID,
	}

	idleTimeoutSec := int(DefaultIdleTimeout.Seconds())
	if req.IdleTimeoutSec > 0 {
		idleTimeoutSec = req.IdleTimeoutSec
	}
	maxLifetimeSec := int(DefaultMaxLifetime.Seconds())
	if req.MaxLifetimeSec > 0 {
		maxLifetimeSec = req.MaxLifetimeSec
	}
	maxExpiresAt := now.Add(time.Duration(maxLifetimeSec) * time.Second)

	annotations := map[string]string{
		"last_activity_at":        now.Format(time.RFC3339),
		"expires_at":              expiresAt.Format(time.RFC3339),
		"workload/idleTimeoutSec": strconv.Itoa(idleTimeoutSec),
		"workload/maxLifetimeSec": strconv.Itoa(maxLifetimeSec),
		"workload/maxExpiresAt":   maxExpiresAt.Format(time.RFC3339),
	}

	var envVars []v1.EnvVar
	for k, v := range env {
		envVars = append(envVars, v1.EnvVar{Name: k, Value: v})
	}

	resources, err := parseResourceRequirements(req)
	if err != nil {
		return nil, err
	}

	nonRoot := true
	var runAsUser int64 = workloadRunAsUID
	var runAsGroup int64 = workloadRunAsUID
	var fsGroup int64 = workloadRunAsUID
	fsGroupPolicy := v1.FSGroupChangeOnRootMismatch
	var gracePeriod int64 = 30

	return &v1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:        podName,
			Namespace:   h.k8sClient.Namespace(),
			Labels:      labels,
			Annotations: annotations,
		},
		Spec: v1.PodSpec{
			RestartPolicy:                 v1.RestartPolicyNever,
			TerminationGracePeriodSeconds: &gracePeriod,
			AutomountServiceAccountToken:  boolPtr(false),
			SecurityContext: &v1.PodSecurityContext{
				RunAsNonRoot:        &nonRoot,
				RunAsUser:           &runAsUser,
				RunAsGroup:          &runAsGroup,
				FSGroup:             &fsGroup,
				FSGroupChangePolicy: &fsGroupPolicy,
			},
		Containers: []v1.Container{
			{
				Name:       "main",
				Image:      req.Image,
				Command:    command,
				Env:        envVars,
				Resources:  resources,
				WorkingDir: "/workspace",
				SecurityContext: &v1.SecurityContext{
					AllowPrivilegeEscalation: boolPtr(false),
					Capabilities: &v1.Capabilities{
						Drop: []v1.Capability{"ALL"},
					},
					SeccompProfile: &v1.SeccompProfile{
						Type: v1.SeccompProfileTypeRuntimeDefault,
					},
				},
				VolumeMounts: []v1.VolumeMount{
					{
						Name:      "workspace",
						MountPath: "/workspace",
						SubPath:   subPath,
					},
				},
			},
		},
			Volumes: []v1.Volume{
				{
					Name: "workspace",
					VolumeSource: v1.VolumeSource{
						PersistentVolumeClaim: &v1.PersistentVolumeClaimVolumeSource{
							ClaimName: h.pvcName,
						},
					},
				},
			},
		},
	}, nil
}

func (h *Handler) waitForPodDeletion(ctx context.Context, podName string, timeout time.Duration) {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		exists, err := h.k8sClient.PodExists(ctx, podName)
		if err != nil || !exists {
			return
		}
		time.Sleep(1 * time.Second)
	}
}

// PodName returns the pod name for a workload.
func PodName(workloadID string) string {
	return fmt.Sprintf("workload-%s", workloadID)
}

func boolPtr(b bool) *bool {
	return &b
}

func jsonResponse(w http.ResponseWriter, status int, v interface{}) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(v)
}

func jsonError(w http.ResponseWriter, status int, message string) {
	jsonResponse(w, status, map[string]string{"error": message})
}

// isValidK8sName checks if a string is valid for use in a K8S pod name segment.
// Must be lowercase alphanumeric or hyphens, start/end with alphanumeric, max 63 chars.
func isValidK8sName(s string) bool {
	if len(s) == 0 || len(s) > 63 {
		return false
	}
	for i, c := range s {
		if c >= 'a' && c <= 'z' || c >= '0' && c <= '9' {
			continue
		}
		if c == '-' && i > 0 && i < len(s)-1 {
			continue
		}
		return false
	}
	return true
}
