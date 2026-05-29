package workload

import (
	"context"
	"encoding/json"
	stderrors "errors"
	"fmt"
	"log"
	"net/http"
	"path"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/agentsmith-project/agentsmith-sandbox-control-plane/internal/afscp"
	"github.com/agentsmith-project/agentsmith-sandbox-control-plane/internal/httperror"
	"github.com/agentsmith-project/agentsmith-sandbox-control-plane/internal/workloadfacts"
	"github.com/agentsmith-project/agentsmith-sandbox-control-plane/internal/workspacebinding"
	v1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/agentsmith-project/agentsmith-sandbox-control-plane/internal/k8s"
	"github.com/agentsmith-project/agentsmith-sandbox-control-plane/internal/observability"
	retryutil "github.com/agentsmith-project/agentsmith-sandbox-control-plane/internal/retry"
)

var k8sRetryConfig = retryutil.RetryConfig{
	MaxAttempts:    3,
	InitialBackoff: 500 * time.Millisecond,
	MaxBackoff:     5 * time.Second,
	BackoffFactor:  2.0,
}

const (
	workspaceVolumeName         = "workspace"
	workspaceInitContainerName  = "workspace-init"
	workspaceArtifactsDirectory = ".artifacts"

	errorCodeWorkloadReleaseIncomplete = "workload_release_incomplete"
)

type workloadPaths struct {
	mountPath  string
	workingDir string
	readOnly   bool
}

type mountLifecycleClient interface {
	GetOrchestratorMountPlan(ctx context.Context, namespaceID, mountBindingID, correlationID string) (afscp.OrchestratorMountPlan, error)
	HeartbeatWorkloadMountBinding(ctx context.Context, namespaceID, mountBindingID, correlationID, idempotencyKey string) (afscp.OperationEnvelope, error)
	ReleaseWorkloadMountBinding(ctx context.Context, namespaceID, mountBindingID, correlationID, idempotencyKey string) (afscp.OperationEnvelope, error)
	UpdateWorkloadMountStatus(ctx context.Context, namespaceID, mountBindingID, status, reason string, observedAt time.Time, correlationID, idempotencyKey string) (afscp.OperationEnvelope, error)
}

type WorkloadStorageFlushBarrier interface {
	FlushWorkloadMount(ctx context.Context, pod *v1.Pod, mountPath string) error
}

// Handler provides REST endpoints for managed workload pod lifecycle.
type Handler struct {
	k8sClient *k8s.Client
	executor  *k8s.Executor
	options   Options
}

type Options struct {
	DefaultNodeSelector map[string]string
	DefaultTolerations  []v1.Toleration
	AFSCPClient         mountLifecycleClient
	StorageFlushBarrier WorkloadStorageFlushBarrier
	WorkloadFactStore   workloadfacts.Store
}

type responseStatusError struct {
	status  int
	message string
}

var errWorkloadScopeMismatch = stderrors.New("workload pod scope mismatch")

func (e responseStatusError) Error() string {
	return e.message
}

type workloadScope struct {
	WorkspaceID string
	ProjectID   string
	WorkloadID  string
}

func newWorkloadScope(workspaceID, projectID, workloadID string) workloadScope {
	return workloadScope{
		WorkspaceID: strings.TrimSpace(workspaceID),
		ProjectID:   strings.TrimSpace(projectID),
		WorkloadID:  strings.TrimSpace(workloadID),
	}
}

func (s workloadScope) podName() string {
	return workloadfacts.ObjectName("workload", s.WorkspaceID, s.ProjectID, s.WorkloadID)
}

func (s workloadScope) validatePod(pod *v1.Pod) error {
	if pod == nil {
		return fmt.Errorf("%w: pod is required", errWorkloadScopeMismatch)
	}
	if got, want := strings.TrimSpace(pod.GetName()), s.podName(); got != want {
		return fmt.Errorf("%w: pod name %q does not match scoped workload identity %q", errWorkloadScopeMismatch, got, want)
	}
	annotations := pod.GetAnnotations()
	labels := pod.GetLabels()
	if err := requireScopeValue("workspace annotation", annotations["mbos.io/workspace-id"], s.WorkspaceID); err != nil {
		return err
	}
	if err := requireScopeValue("project annotation", annotations["mbos.io/project-id"], s.ProjectID); err != nil {
		return err
	}
	if err := requireScopeValue("workload annotation", annotations["mbos.io/workload-id"], s.WorkloadID); err != nil {
		return err
	}
	if err := requireScopeValue("workspace label", labels["workspace_id"], workloadfacts.LabelValue(s.WorkspaceID)); err != nil {
		return err
	}
	if err := requireScopeValue("project label", labels["project_id"], workloadfacts.LabelValue(s.ProjectID)); err != nil {
		return err
	}
	if err := requireScopeValue("workload label", labels["workload_id"], workloadfacts.LabelValue(s.WorkloadID)); err != nil {
		return err
	}
	return nil
}

func requireScopeValue(name, got, want string) error {
	got = strings.TrimSpace(got)
	want = strings.TrimSpace(want)
	if got == "" {
		return fmt.Errorf("%w: %s is missing", errWorkloadScopeMismatch, name)
	}
	if got != want {
		return fmt.Errorf("%w: %s mismatch", errWorkloadScopeMismatch, name)
	}
	return nil
}

// NewHandler creates a new workload handler.
func NewHandler(k8sClient *k8s.Client, executor *k8s.Executor, opts ...Options) *Handler {
	var option Options
	if len(opts) > 0 {
		option = opts[0]
	}
	return &Handler{
		k8sClient: k8sClient,
		executor:  executor,
		options:   option,
	}
}

// RegisterRoutes registers workload HTTP routes on the given mux.
func (h *Handler) RegisterRoutes(mux *http.ServeMux) {
	mux.HandleFunc("/v1/workspaces/", h.routeRequest)
}

func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	h.routeRequest(w, r)
}

// parseRoute extracts workspaceID, projectID, workloadID, and action from the URL path.
// Path format: /v1/workspaces/{wsId}/projects/{projId}/workloads/{wlId}[/keepalive|/exec]
func parseRoute(path string) (workspaceID, projectID, workloadID, action string, ok bool) {
	trimmed := strings.TrimPrefix(path, "/v1/workspaces/")
	if trimmed == path {
		return
	}

	parts := strings.Split(trimmed, "/")
	if len(parts) != 5 && len(parts) != 6 {
		return
	}
	if parts[1] != "projects" || parts[3] != "workloads" {
		return
	}

	workspaceID = parts[0]
	projectID = parts[2]
	workloadID = parts[4]

	if len(parts) == 6 {
		if parts[5] == "" {
			return
		}
		action = parts[5]
	}

	ok = workspaceID != "" && projectID != "" && workloadID != ""
	return
}

func (h *Handler) routeRequest(w http.ResponseWriter, r *http.Request) {
	workspaceID, projectID, workloadID, action, ok := parseRoute(r.URL.Path)
	if !ok {
		jsonError(w, r, http.StatusNotFound, "not_found", "not found")
		return
	}

	if !isValidK8sName(workloadID) {
		jsonError(w, r, http.StatusBadRequest, "invalid_request", "invalid workload_id: must be lowercase alphanumeric or hyphens, max 63 chars")
		return
	}

	switch {
	case action == "" && r.Method == http.MethodPut:
		h.handleCreatePod(w, r, workspaceID, projectID, workloadID)
	case action == "" && r.Method == http.MethodDelete:
		h.handleDeletePod(w, r, workspaceID, projectID, workloadID)
	case action == "" && r.Method == http.MethodGet:
		h.handleGetPod(w, r, workspaceID, projectID, workloadID)
	case action == "keepalive" && r.Method == http.MethodPost:
		h.handleKeepalive(w, r, workspaceID, projectID, workloadID)
	case action == "exec" && r.Method == http.MethodPost:
		h.handleExec(w, r, workspaceID, projectID, workloadID)
	default:
		jsonError(w, r, http.StatusMethodNotAllowed, "method_not_allowed", "method not allowed")
	}
}

func (h *Handler) handleCreatePod(w http.ResponseWriter, r *http.Request, workspaceID, projectID, workloadID string) {
	var req CreateRequest
	if err := decodeCreateRequest(r, &req); err != nil {
		jsonError(w, r, http.StatusBadRequest, "invalid_request", "invalid request body: "+err.Error())
		return
	}

	if req.Image == "" {
		jsonError(w, r, http.StatusBadRequest, "invalid_request", "image is required")
		return
	}
	if req.WorkspaceBindingID == "" {
		jsonError(w, r, http.StatusBadRequest, "invalid_request", "workspace_binding_id is required")
		return
	}
	if _, err := parseResourceRequirements(req); err != nil {
		jsonError(w, r, http.StatusBadRequest, "invalid_request", err.Error())
		return
	}

	ctx := r.Context()
	mount, err := h.resolveWorkspaceMount(ctx, r, workspaceID, projectID, workloadID, req.WorkspaceBindingID)
	if err != nil {
		status := statusCodeForError(err, http.StatusBadRequest)
		jsonError(w, r, status, httperror.CodeForStatus(status), err.Error())
		return
	}
	req.resolvedMount = &mount

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

	scope := newWorkloadScope(workspaceID, projectID, workloadID)
	podName := scope.podName()

	env := make(map[string]string)
	for k, v := range req.Env {
		env[k] = v
	}

	pod, err := h.buildPod(workspaceID, projectID, workloadID, podName, env, req, now, expiresAt)
	if err != nil {
		jsonError(w, r, http.StatusBadRequest, "invalid_request", err.Error())
		return
	}

	createdPod, err := h.k8sClient.Clientset().CoreV1().Pods(h.k8sClient.Namespace()).Create(ctx, pod, metav1.CreateOptions{})
	if err != nil {
		if apierrors.IsAlreadyExists(err) {
			existingPod, getErr := h.getScopedWorkloadPod(ctx, scope)
			if getErr == nil {
				if drift := workloadPodSpecDrift(existingPod, pod); drift != "" {
					jsonError(w, r, http.StatusConflict, "conflict", "existing pod spec drift: "+drift)
					return
				}
				if err := h.recordWorkloadFact(ctx, workspaceID, projectID, workloadID, req.WorkspaceBindingID, existingPod); err != nil {
					log.Printf("workload/%s: workload fact write failed: %s", workloadID, observability.RedactLogValue(err))
					jsonError(w, r, http.StatusInternalServerError, "internal_error", "workload terminal fact write failed")
					return
				}
				jsonResponse(w, http.StatusOK, PodStatus{
					WorkloadID:    workloadID,
					PodName:       existingPod.Name,
					Status:        workloadStatusForPod(existingPod),
					Phase:         podPhaseString(existingPod),
					IP:            existingPod.Status.PodIP,
					Image:         mainContainerImage(existingPod),
					ImageRef:      mainContainerImage(existingPod),
					ImageID:       mainContainerImageID(existingPod),
					StartedAt:     existingPod.CreationTimestamp.Format(time.RFC3339),
					CorrelationID: requestCorrelationID(r),
					Message:       "pod already exists",
				})
				return
			}
			if stderrors.Is(getErr, errWorkloadScopeMismatch) {
				log.Printf("workload/%s: existing pod scope mismatch: %s", workloadID, observability.RedactLogValue(getErr))
				jsonError(w, r, http.StatusConflict, "conflict", "workload pod scope mismatch")
				return
			}
		}
		log.Printf("workload/%s: pod creation failed: %s", workloadID, observability.RedactLogValue(err))
		jsonError(w, r, http.StatusInternalServerError, "internal_error", "pod creation failed")
		return
	}

	if err := h.recordWorkloadFact(ctx, workspaceID, projectID, workloadID, req.WorkspaceBindingID, createdPod); err != nil {
		log.Printf("workload/%s: workload fact write failed: %s", workloadID, observability.RedactLogValue(err))
		h.rollbackCreatedPodAfterFactFailure(ctx, workloadID, createdPod.GetName())
		jsonError(w, r, http.StatusInternalServerError, "internal_error", "workload terminal fact write failed")
		return
	}

	status := PodStatus{
		WorkloadID:    workloadID,
		PodName:       createdPod.Name,
		Status:        workloadStatusForPod(createdPod),
		Phase:         podPhaseString(createdPod),
		Image:         mainContainerImage(createdPod),
		ImageRef:      mainContainerImage(createdPod),
		ImageID:       mainContainerImageID(createdPod),
		StartedAt:     createdPod.CreationTimestamp.Format(time.RFC3339),
		ExpiresAt:     expiresAt.Format(time.RFC3339),
		CorrelationID: requestCorrelationID(r),
	}

	observability.GetMetrics().RecordWorkloadCreate()
	jsonResponse(w, http.StatusCreated, status)
}

func decodeCreateRequest(r *http.Request, req *CreateRequest) error {
	decoder := json.NewDecoder(r.Body)
	decoder.DisallowUnknownFields()
	return decoder.Decode(req)
}

func (h *Handler) handleDeletePod(w http.ResponseWriter, r *http.Request, workspaceID, projectID, workloadID string) {
	ctx := r.Context()
	scope := newWorkloadScope(workspaceID, projectID, workloadID)
	podName := scope.podName()
	store := h.workloadFactStore()

	fact, hasFact, err := h.loadWorkloadFact(ctx, store, workspaceID, projectID, workloadID)
	if err != nil {
		log.Printf("workload/%s: workload fact lookup failed: %s", workloadID, observability.RedactLogValue(err))
		jsonError(w, r, http.StatusInternalServerError, "internal_error", "workload terminal fact lookup failed")
		return
	}
	if hasFact && fact.Terminal() {
		if _, err := h.getScopedWorkloadPod(ctx, scope); err == nil {
			jsonError(w, r, http.StatusConflict, errorCodeWorkloadReleaseIncomplete, "workload terminal fact says pod deleted but scoped pod still exists")
			return
		} else if apierrors.IsNotFound(err) {
			observability.GetMetrics().RecordWorkloadDelete()
			jsonResponse(w, http.StatusOK, DeleteResponse{Message: "pod deleted"})
			return
		} else if stderrors.Is(err, errWorkloadScopeMismatch) {
			log.Printf("workload/%s: pod scope mismatch before terminal delete retry: %s", workloadID, observability.RedactLogValue(err))
			jsonError(w, r, http.StatusConflict, "conflict", "workload pod scope mismatch")
			return
		} else {
			log.Printf("workload/%s: pod lookup failed before terminal delete retry: %s", workloadID, observability.RedactLogValue(err))
			jsonError(w, r, http.StatusInternalServerError, "internal_error", "pod lookup failed")
			return
		}
	}

	var pod *v1.Pod
	podMissing := false
	if !hasFact || !fact.PodDeleted {
		pod, err = h.getScopedWorkloadPod(ctx, scope)
		if err != nil {
			if apierrors.IsNotFound(err) {
				podMissing = true
			} else if stderrors.Is(err, errWorkloadScopeMismatch) {
				log.Printf("workload/%s: pod scope mismatch before delete: %s", workloadID, observability.RedactLogValue(err))
				jsonError(w, r, http.StatusConflict, "conflict", "workload pod scope mismatch")
				return
			} else {
				log.Printf("workload/%s: pod lookup failed: %s", workloadID, observability.RedactLogValue(err))
				jsonError(w, r, http.StatusInternalServerError, "internal_error", "pod lookup failed")
				return
			}
		}
	}

	if !hasFact {
		if podMissing {
			jsonError(w, r, http.StatusConflict, errorCodeWorkloadReleaseIncomplete, "workload terminal fact is missing; pod absence alone is not terminal truth")
			return
		}
		fact, err = h.workloadFactFromPod(workspaceID, projectID, workloadID, "", pod)
		if err != nil {
			logWorkloadMountDependencyFailure(r, "workload terminal fact initialization failed", err, workspaceID, projectID, workloadID, pod)
			jsonError(w, r, http.StatusInternalServerError, "internal_error", "workload terminal fact initialization failed")
			return
		}
		if err := store.Save(ctx, fact); err != nil {
			log.Printf("workload/%s: workload fact write failed: %s", workloadID, observability.RedactLogValue(err))
			jsonError(w, r, http.StatusInternalServerError, "internal_error", "workload terminal fact write failed")
			return
		}
		hasFact = true
	}

	storageFlushed := false
	if !fact.ReleaseDone {
		if !podMissing && pod != nil {
			if err := h.flushWorkloadStorage(ctx, pod); err != nil {
				log.Printf("workload/%s: storage flush barrier failed: %s", workloadID, observability.RedactLogValue(err))
				jsonError(w, r, http.StatusInternalServerError, "internal_error", "storage flush barrier failed")
				return
			}
			storageFlushed = true
		}

		if err := h.releaseWorkloadMountFromFact(ctx, r, fact); err != nil {
			logWorkloadFactDependencyFailure(r, "AFSCP workload mount release failed", err, workspaceID, projectID, workloadID, fact)
			jsonError(w, r, http.StatusBadGateway, "dependency_failure", "AFSCP workload mount release failed")
			return
		}
		fact.ReleaseDone = true
		if err := store.Save(ctx, fact); err != nil {
			log.Printf("workload/%s: workload release fact write failed: %s", workloadID, observability.RedactLogValue(err))
			jsonError(w, r, http.StatusInternalServerError, "internal_error", "workload terminal fact write failed")
			return
		}
	}

	if !fact.PodDeleted {
		if podMissing {
			fact.PodDeleted = true
			if err := store.Save(ctx, fact); err != nil {
				log.Printf("workload/%s: workload pod-deleted fact write failed: %s", workloadID, observability.RedactLogValue(err))
				jsonError(w, r, http.StatusInternalServerError, "internal_error", "workload terminal fact write failed")
				return
			}
		} else {
			if !storageFlushed {
				if err := h.flushWorkloadStorage(ctx, pod); err != nil {
					log.Printf("workload/%s: storage flush barrier failed: %s", workloadID, observability.RedactLogValue(err))
					jsonError(w, r, http.StatusInternalServerError, "internal_error", "storage flush barrier failed")
					return
				}
			}

			if err := retryutil.Retry(ctx, k8sRetryConfig, func() error {
				return h.k8sClient.DeletePod(ctx, podName, 10)
			}); err != nil {
				log.Printf("workload/%s: pod deletion failed: %s", workloadID, observability.RedactLogValue(err))
				jsonError(w, r, http.StatusInternalServerError, "internal_error", "pod deletion failed")
				return
			}

			if err := h.waitForPodDeletion(ctx, podName, 30*time.Second); err != nil {
				log.Printf("workload/%s: pod deletion not confirmed: %s", workloadID, observability.RedactLogValue(err))
				jsonError(w, r, http.StatusInternalServerError, "internal_error", "pod deletion not confirmed")
				return
			}

			fact.PodDeleted = true
			if err := store.Save(ctx, fact); err != nil {
				log.Printf("workload/%s: workload pod-deleted fact write failed: %s", workloadID, observability.RedactLogValue(err))
				jsonError(w, r, http.StatusInternalServerError, "internal_error", "workload terminal fact write failed")
				return
			}
		}
	}

	if !fact.TerminalStatusDone {
		if err := h.markWorkloadMountReleasedFromFact(ctx, r, fact); err != nil {
			logWorkloadFactDependencyFailure(r, "AFSCP workload mount released status failed", err, workspaceID, projectID, workloadID, fact)
			jsonError(w, r, http.StatusBadGateway, "dependency_failure", "AFSCP workload mount released status failed")
			return
		}
		fact.TerminalStatusDone = true
		if err := store.Save(ctx, fact); err != nil {
			log.Printf("workload/%s: workload terminal-status fact write failed: %s", workloadID, observability.RedactLogValue(err))
			jsonError(w, r, http.StatusInternalServerError, "internal_error", "workload terminal fact write failed")
			return
		}
	}

	if !fact.Terminal() {
		jsonError(w, r, http.StatusConflict, errorCodeWorkloadReleaseIncomplete, "workload release is incomplete")
		return
	}

	observability.GetMetrics().RecordWorkloadDelete()
	jsonResponse(w, http.StatusOK, DeleteResponse{Message: "pod deleted"})
}

func (h *Handler) handleGetPod(w http.ResponseWriter, r *http.Request, workspaceID, projectID, workloadID string) {
	ctx := r.Context()
	scope := newWorkloadScope(workspaceID, projectID, workloadID)

	pod, err := h.getScopedWorkloadPod(ctx, scope)
	if err != nil {
		if apierrors.IsNotFound(err) {
			jsonResponse(w, http.StatusOK, PodStatus{Status: "offline", Phase: "offline"})
			return
		}
		if stderrors.Is(err, errWorkloadScopeMismatch) {
			log.Printf("workload/%s: pod scope mismatch before get: %s", workloadID, observability.RedactLogValue(err))
			jsonError(w, r, http.StatusConflict, "conflict", "workload pod scope mismatch")
			return
		}
		log.Printf("workload/%s: pod lookup failed: %s", workloadID, observability.RedactLogValue(err))
		jsonError(w, r, http.StatusInternalServerError, "internal_error", "pod lookup failed")
		return
	}

	status := PodStatus{
		PodName:   pod.Name,
		Status:    workloadStatusForPod(pod),
		Phase:     podPhaseString(pod),
		IP:        pod.Status.PodIP,
		Image:     mainContainerImage(pod),
		ImageRef:  mainContainerImage(pod),
		ImageID:   mainContainerImageID(pod),
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
// periodically; expired workloads must be released through the workload delete API.
func (h *Handler) handleKeepalive(w http.ResponseWriter, r *http.Request, workspaceID, projectID, workloadID string) {
	ctx := r.Context()
	scope := newWorkloadScope(workspaceID, projectID, workloadID)
	podName := scope.podName()

	pod, err := h.getScopedWorkloadPod(ctx, scope)
	if err != nil {
		if apierrors.IsNotFound(err) {
			jsonError(w, r, http.StatusNotFound, "not_found", "pod not found")
			return
		}
		if stderrors.Is(err, errWorkloadScopeMismatch) {
			log.Printf("workload/%s: pod scope mismatch before keepalive: %s", workloadID, observability.RedactLogValue(err))
			jsonError(w, r, http.StatusConflict, "conflict", "workload pod scope mismatch")
			return
		}
		log.Printf("workload/%s: pod lookup failed: %s", workloadID, observability.RedactLogValue(err))
		jsonError(w, r, http.StatusInternalServerError, "internal_error", "pod lookup failed")
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

	if err := h.heartbeatWorkloadMount(ctx, r, pod); err != nil {
		logWorkloadMountDependencyFailure(r, "AFSCP workload mount heartbeat failed", err, "", "", workloadID, pod)
		jsonError(w, r, http.StatusBadGateway, "dependency_failure", "AFSCP workload mount heartbeat failed")
		return
	}

	if err := retryutil.Retry(ctx, k8sRetryConfig, func() error {
		return h.k8sClient.PatchActivity(ctx, podName, newExpires)
	}); err != nil {
		log.Printf("workload/%s: failed to update keepalive: %s", workloadID, observability.RedactLogValue(err))
		jsonError(w, r, http.StatusInternalServerError, "internal_error", "failed to update keepalive")
		return
	}

	observability.GetMetrics().RecordWorkloadKeepalive()
	jsonResponse(w, http.StatusOK, KeepaliveResponse{
		ExpiresAt: newExpires.Format(time.RFC3339),
	})
}

func (h *Handler) handleExec(w http.ResponseWriter, r *http.Request, workspaceID, projectID, workloadID string) {
	var req ExecRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		jsonError(w, r, http.StatusBadRequest, "invalid_request", "invalid request body: "+err.Error())
		return
	}
	if len(req.Cmd) == 0 {
		jsonError(w, r, http.StatusBadRequest, "invalid_request", "cmd is required")
		return
	}

	ctx := r.Context()
	scope := newWorkloadScope(workspaceID, projectID, workloadID)
	podName := scope.podName()

	_, err := h.getScopedWorkloadPod(ctx, scope)
	if err != nil {
		if apierrors.IsNotFound(err) {
			jsonError(w, r, http.StatusNotFound, "not_found", "pod not found")
			return
		}
		if stderrors.Is(err, errWorkloadScopeMismatch) {
			log.Printf("workload/%s: pod scope mismatch before exec: %s", workloadID, observability.RedactLogValue(err))
			jsonError(w, r, http.StatusConflict, "conflict", "workload pod scope mismatch")
			return
		}
		log.Printf("workload/%s: pod lookup failed before exec: %s", workloadID, observability.RedactLogValue(err))
		jsonError(w, r, http.StatusInternalServerError, "internal_error", "pod lookup failed")
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
		log.Printf("workload/%s: exec failed: %s", workloadID, observability.RedactLogValue(err))
		jsonError(w, r, http.StatusInternalServerError, "internal_error", "exec failed")
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
	workspaceID, projectID, workloadID, podName string,
	env map[string]string,
	req CreateRequest,
	now time.Time,
	expiresAt time.Time,
) (*v1.Pod, error) {
	labels := map[string]string{
		"app":          WorkloadLabel,
		"workload_id":  workloadfacts.LabelValue(workloadID),
		"workspace_id": workloadfacts.LabelValue(workspaceID),
		"project_id":   workloadfacts.LabelValue(projectID),
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
		"mbos.io/workspace-id":    workspaceID,
		"mbos.io/project-id":      projectID,
		"mbos.io/workload-id":     workloadID,
	}

	paths, err := resolveWorkloadPaths(req)
	if err != nil {
		return nil, err
	}
	if req.resolvedMount != nil {
		annotations["mbos.io/afscp-namespace-id"] = req.resolvedMount.NamespaceID
		annotations["mbos.io/afscp-mount-binding-id"] = req.resolvedMount.MountBindingID
		annotations["mbos.io/afscp-volume-id"] = req.resolvedMount.VolumeID
		annotations["mbos.io/payload-volume-subdir"] = req.resolvedMount.PayloadVolumeSubdir
		annotations["mbos.io/mount-path"] = req.resolvedMount.MountPath
		annotations["mbos.io/read-only"] = boolString(req.resolvedMount.ReadOnly)
	}
	envVars := buildRuntimeEnvVars(env, paths)

	var command []string
	if len(req.Command) > 0 {
		command = req.Command
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

	pod := &v1.Pod{
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
			NodeSelector:                  h.options.DefaultNodeSelector,
			Tolerations:                   h.options.DefaultTolerations,
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
					Env:        envVars,
					Resources:  resources,
					WorkingDir: paths.workingDir,
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
						workspaceVolumeMount(paths),
					},
				},
			},
			Volumes: []v1.Volume{
				{
					Name: workspaceVolumeName,
					VolumeSource: v1.VolumeSource{
						PersistentVolumeClaim: &v1.PersistentVolumeClaimVolumeSource{
							ClaimName: workspacebinding.PVCName(workspaceID, projectID, req.WorkspaceBindingID),
							ReadOnly:  paths.readOnly,
						},
					},
				},
			},
		},
	}

	if len(command) > 0 {
		pod.Spec.Containers[0].Command = command
	}
	if !paths.readOnly {
		pod.Spec.InitContainers = []v1.Container{
			buildWorkspaceInitContainer(req.Image, paths),
		}
	}

	return pod, nil
}

func buildWorkspaceInitContainer(image string, paths workloadPaths) v1.Container {
	nonRoot := false
	var runAsUser int64 = 0
	var runAsGroup int64 = workloadRunAsUID

	return v1.Container{
		Name:       workspaceInitContainerName,
		Image:      image,
		WorkingDir: paths.mountPath,
		Command: []string{
			"sh",
			"-ceu",
			fmt.Sprintf(`umask 0007
mkdir -p "$TASK_HOME" "$WORKSPACE_PATH" "$ARTIFACTS_PATH"
for dir in "$TASK_HOME" "$WORKSPACE_PATH" "$ARTIFACTS_PATH"; do
  chgrp %d "$dir"
  chmod g+rwx "$dir"
  test -w "$dir"
done`, workloadRunAsUID),
		},
		Env: []v1.EnvVar{
			{Name: "ARTIFACTS_PATH", Value: path.Join(paths.workingDir, workspaceArtifactsDirectory)},
			{Name: "TASK_HOME", Value: paths.mountPath},
			{Name: "WORKSPACE_PATH", Value: paths.workingDir},
		},
		SecurityContext: &v1.SecurityContext{
			RunAsNonRoot:             &nonRoot,
			RunAsUser:                &runAsUser,
			RunAsGroup:               &runAsGroup,
			AllowPrivilegeEscalation: boolPtr(false),
			Capabilities: &v1.Capabilities{
				Drop: []v1.Capability{"ALL"},
			},
			SeccompProfile: &v1.SeccompProfile{
				Type: v1.SeccompProfileTypeRuntimeDefault,
			},
		},
		VolumeMounts: []v1.VolumeMount{
			workspaceVolumeMount(paths),
		},
	}
}

func workspaceVolumeMount(paths workloadPaths) v1.VolumeMount {
	return v1.VolumeMount{
		Name:      workspaceVolumeName,
		MountPath: paths.mountPath,
		ReadOnly:  paths.readOnly,
	}
}

func resolveWorkloadPaths(req CreateRequest) (workloadPaths, error) {
	if req.resolvedMount == nil {
		return workloadPaths{}, fmt.Errorf("AFSCP workload mount plan is required")
	}

	mountPath := strings.TrimSpace(req.resolvedMount.MountPath)
	if mountPath == "" || !path.IsAbs(mountPath) || mountPath == "/" || containsParentPathSegment(mountPath) || path.Clean(mountPath) != mountPath {
		return workloadPaths{}, fmt.Errorf("AFSCP mount_path is invalid")
	}
	if strings.Contains(mountPath, "\\") {
		return workloadPaths{}, fmt.Errorf("AFSCP mount_path is invalid")
	}

	return workloadPaths{
		mountPath:  mountPath,
		workingDir: path.Join(mountPath, "workspace"),
		readOnly:   req.resolvedMount.ReadOnly,
	}, nil
}

func (h *Handler) resolveWorkspaceMount(ctx context.Context, r *http.Request, workspaceID, projectID, workloadID, bindingID string) (workspacebinding.ResolvedMount, error) {
	pvcName := workspacebinding.PVCName(workspaceID, projectID, bindingID)
	pvc, err := h.k8sClient.GetPersistentVolumeClaim(ctx, h.k8sClient.Namespace(), pvcName)
	if err != nil {
		return workspacebinding.ResolvedMount{}, responseStatusError{
			status:  http.StatusConflict,
			message: "workspace binding is not ready; re-ensure workspace binding",
		}
	}
	mount, err := workspacebinding.ResolvedMountFromPVC(pvc)
	if err != nil {
		return workspacebinding.ResolvedMount{}, responseStatusError{
			status:  http.StatusConflict,
			message: "workspace binding mount plan is invalid; re-ensure workspace binding",
		}
	}
	pvName := strings.TrimSpace(pvc.Spec.VolumeName)
	if pvName == "" {
		return workspacebinding.ResolvedMount{}, responseStatusError{
			status:  http.StatusConflict,
			message: "workspace binding is stale: persistent volume is not bound; re-ensure workspace binding",
		}
	}
	pv, err := h.k8sClient.GetPersistentVolume(ctx, pvName)
	if err != nil {
		return workspacebinding.ResolvedMount{}, responseStatusError{
			status:  http.StatusConflict,
			message: "workspace binding is stale: persistent volume is not ready; re-ensure workspace binding",
		}
	}
	if err := ensurePersistentVolumeMatchesResolvedMount(pv, mount); err != nil {
		return workspacebinding.ResolvedMount{}, responseStatusError{
			status:  http.StatusConflict,
			message: "workspace binding is stale: " + err.Error() + "; re-ensure workspace binding",
		}
	}
	if h.options.AFSCPClient != nil {
		correlationID := requestCorrelationID(r)
		plan, err := h.options.AFSCPClient.GetOrchestratorMountPlan(ctx, mount.NamespaceID, mount.MountBindingID, correlationID)
		if err != nil {
			log.Printf("workload/%s: workspace binding active check failed: workspace=%s project=%s workload=%s binding=%s namespace_id=%s mount_binding_id=%s request_id=%s correlation_id=%s error=%s",
				workloadID,
				workspaceID,
				projectID,
				workloadID,
				bindingID,
				mount.NamespaceID,
				mount.MountBindingID,
				observability.GetRequestID(r),
				correlationID,
				observability.RedactLogValue(err),
			)
			return workspacebinding.ResolvedMount{}, responseStatusError{
				status:  http.StatusBadGateway,
				message: "workspace binding active check failed",
			}
		}
		if err := ensureResolvedMountMatchesPlan(mount, plan); err != nil {
			return workspacebinding.ResolvedMount{}, responseStatusError{
				status:  http.StatusConflict,
				message: "workspace binding is stale: " + err.Error() + "; re-ensure workspace binding",
			}
		}
		if err := ensurePersistentVolumeMatchesPlan(pv, plan); err != nil {
			return workspacebinding.ResolvedMount{}, responseStatusError{
				status:  http.StatusConflict,
				message: "workspace binding is stale: " + err.Error() + "; re-ensure workspace binding",
			}
		}
	}
	return mount, nil
}

func ensureResolvedMountMatchesPlan(mount workspacebinding.ResolvedMount, plan afscp.OrchestratorMountPlan) error {
	checks := []struct {
		name string
		got  string
		want string
	}{
		{name: "mount_binding_id", got: mount.MountBindingID, want: plan.MountBindingID},
		{name: "volume_id", got: mount.VolumeID, want: plan.VolumeID},
		{name: "payload_volume_subdir", got: mount.PayloadVolumeSubdir, want: plan.PayloadVolumeSubdir},
		{name: "mount_path", got: mount.MountPath, want: plan.MountPath},
	}
	for _, check := range checks {
		if strings.TrimSpace(check.got) != strings.TrimSpace(check.want) {
			return fmt.Errorf("%s changed", check.name)
		}
	}
	if mount.ReadOnly != plan.ReadOnly {
		return fmt.Errorf("read_only changed")
	}
	if mount.SecurityPolicy.RunAsNonRoot != plan.SecurityPolicy.RunAsNonRoot ||
		mount.SecurityPolicy.AllowPrivileged != plan.SecurityPolicy.AllowPrivileged ||
		mount.SecurityPolicy.JVSControlOutsidePayload != plan.SecurityPolicy.JVSControlOutsidePayload {
		return fmt.Errorf("security_policy changed")
	}
	return nil
}

func ensurePersistentVolumeMatchesResolvedMount(pv *v1.PersistentVolume, mount workspacebinding.ResolvedMount) error {
	if pv == nil {
		return fmt.Errorf("persistent volume is required")
	}
	if pv.Spec.CSI == nil {
		return fmt.Errorf("persistent volume is not CSI-backed")
	}
	if err := ensurePersistentVolumePayloadMountOption(pv, mount.PayloadVolumeSubdir); err != nil {
		return err
	}
	return nil
}

func ensurePersistentVolumeMatchesPlan(pv *v1.PersistentVolume, plan afscp.OrchestratorMountPlan) error {
	if pv == nil {
		return fmt.Errorf("persistent volume is required")
	}
	if pv.Spec.CSI == nil {
		return fmt.Errorf("persistent volume is not CSI-backed")
	}
	if err := ensurePersistentVolumePayloadMountOption(pv, plan.PayloadVolumeSubdir); err != nil {
		return err
	}
	ref := pv.Spec.CSI.NodePublishSecretRef
	if ref == nil ||
		strings.TrimSpace(ref.Namespace) != strings.TrimSpace(plan.SecretRef.Namespace) ||
		strings.TrimSpace(ref.Name) != strings.TrimSpace(plan.SecretRef.Name) {
		return fmt.Errorf("persistent volume secret_ref changed")
	}
	return nil
}

func ensurePersistentVolumePayloadMountOption(pv *v1.PersistentVolume, payloadVolumeSubdir string) error {
	want := "subdir=" + strings.TrimSpace(payloadVolumeSubdir)
	found := false
	for _, option := range pv.Spec.MountOptions {
		trimmed := strings.TrimSpace(option)
		if !strings.HasPrefix(trimmed, "subdir=") {
			continue
		}
		if trimmed != want {
			return fmt.Errorf("persistent volume payload_volume_subdir changed")
		}
		if found {
			return fmt.Errorf("persistent volume payload_volume_subdir mount option duplicated")
		}
		found = true
	}
	if !found {
		return fmt.Errorf("persistent volume payload_volume_subdir mount option missing")
	}
	return nil
}

func statusCodeForError(err error, fallback int) int {
	var statusErr responseStatusError
	if stderrors.As(err, &statusErr) {
		return statusErr.status
	}
	return fallback
}

func buildRuntimeEnvVars(env map[string]string, paths workloadPaths) []v1.EnvVar {
	envMap := make(map[string]string, len(env)+3)
	for k, v := range env {
		envMap[k] = v
	}
	envMap["TASK_HOME"] = paths.mountPath
	envMap["HOME"] = paths.mountPath
	envMap["WORKSPACE_PATH"] = paths.workingDir

	keys := make([]string, 0, len(envMap))
	for k := range envMap {
		keys = append(keys, k)
	}
	sort.Strings(keys)

	envVars := make([]v1.EnvVar, 0, len(keys))
	for _, k := range keys {
		envVars = append(envVars, v1.EnvVar{Name: k, Value: envMap[k]})
	}
	return envVars
}

func containsParentPathSegment(value string) bool {
	for _, part := range strings.Split(value, "/") {
		if part == ".." {
			return true
		}
	}
	return false
}

func workloadPodSpecDrift(existing, desired *v1.Pod) string {
	existingContainer, ok := findContainer(existing.Spec.Containers, "main")
	if !ok {
		return "existing pod is missing main container"
	}
	desiredContainer, ok := findContainer(desired.Spec.Containers, "main")
	if !ok {
		return "requested pod is missing main container"
	}

	if existingContainer.WorkingDir != desiredContainer.WorkingDir {
		return fmt.Sprintf("working directory mismatch (existing %q, requested %q)", existingContainer.WorkingDir, desiredContainer.WorkingDir)
	}

	existingMount, ok := findVolumeMount(existingContainer.VolumeMounts, workspaceVolumeName)
	if !ok {
		return "existing pod is missing workspace volume mount"
	}
	desiredMount, ok := findVolumeMount(desiredContainer.VolumeMounts, workspaceVolumeName)
	if !ok {
		return "requested pod is missing workspace volume mount"
	}
	if existingMount.MountPath != desiredMount.MountPath {
		return fmt.Sprintf("mount path mismatch (existing %q, requested %q)", existingMount.MountPath, desiredMount.MountPath)
	}
	if existingMount.SubPath != desiredMount.SubPath {
		return fmt.Sprintf("workspace volume subpath mismatch (existing %q, requested %q)", existingMount.SubPath, desiredMount.SubPath)
	}

	existingClaim := pvcClaimName(existing.Spec.Volumes, workspaceVolumeName)
	desiredClaim := pvcClaimName(desired.Spec.Volumes, workspaceVolumeName)
	if existingClaim == "" {
		return "existing pod is missing workspace PVC volume"
	}
	if desiredClaim == "" {
		return "requested pod is missing workspace PVC volume"
	}
	if existingClaim != desiredClaim {
		return fmt.Sprintf("workspace PVC mismatch (existing %q, requested %q)", existingClaim, desiredClaim)
	}

	existingEnv := envVarMap(existingContainer.Env)
	desiredEnv := envVarMap(desiredContainer.Env)
	for _, key := range []string{"TASK_HOME", "HOME", "WORKSPACE_PATH"} {
		if existingEnv[key] != desiredEnv[key] {
			return fmt.Sprintf("%s env mismatch (existing %q, requested %q)", key, existingEnv[key], desiredEnv[key])
		}
	}

	if drift := workspaceInitContainerSpecDrift(existing, desired); drift != "" {
		return drift
	}

	return ""
}

func workspaceInitContainerSpecDrift(existing, desired *v1.Pod) string {
	existingInit, ok := findContainer(existing.Spec.InitContainers, workspaceInitContainerName)
	desiredInit, desiredOK := findContainer(desired.Spec.InitContainers, workspaceInitContainerName)
	if !desiredOK {
		if ok {
			return "existing pod has workspace init container but requested read-only plan does not"
		}
		return ""
	}
	if !ok {
		return "existing pod is missing workspace init container"
	}

	if existingInit.WorkingDir != desiredInit.WorkingDir {
		return fmt.Sprintf("workspace init working directory mismatch (existing %q, requested %q)", existingInit.WorkingDir, desiredInit.WorkingDir)
	}

	existingMount, ok := findVolumeMount(existingInit.VolumeMounts, workspaceVolumeName)
	if !ok {
		return "existing pod is missing workspace init volume mount"
	}
	desiredMount, ok := findVolumeMount(desiredInit.VolumeMounts, workspaceVolumeName)
	if !ok {
		return "requested pod is missing workspace init volume mount"
	}
	if existingMount.MountPath != desiredMount.MountPath {
		return fmt.Sprintf("workspace init mount path mismatch (existing %q, requested %q)", existingMount.MountPath, desiredMount.MountPath)
	}
	if existingMount.SubPath != desiredMount.SubPath {
		return fmt.Sprintf("workspace init volume subpath mismatch (existing %q, requested %q)", existingMount.SubPath, desiredMount.SubPath)
	}

	existingEnv := envVarMap(existingInit.Env)
	desiredEnv := envVarMap(desiredInit.Env)
	for _, key := range []string{"TASK_HOME", "WORKSPACE_PATH", "ARTIFACTS_PATH"} {
		if existingEnv[key] != desiredEnv[key] {
			return fmt.Sprintf("workspace init %s env mismatch (existing %q, requested %q)", key, existingEnv[key], desiredEnv[key])
		}
	}

	return ""
}

func findContainer(containers []v1.Container, name string) (v1.Container, bool) {
	for _, container := range containers {
		if container.Name == name {
			return container, true
		}
	}
	return v1.Container{}, false
}

func mainContainerImage(pod *v1.Pod) string {
	if pod == nil {
		return ""
	}
	container, ok := findContainer(pod.Spec.Containers, "main")
	if !ok {
		return ""
	}
	return container.Image
}

func mainContainerImageID(pod *v1.Pod) string {
	if pod == nil {
		return ""
	}
	for _, status := range pod.Status.ContainerStatuses {
		if status.Name == "main" {
			return status.ImageID
		}
	}
	return ""
}

func findVolumeMount(mounts []v1.VolumeMount, name string) (v1.VolumeMount, bool) {
	for _, mount := range mounts {
		if mount.Name == name {
			return mount, true
		}
	}
	return v1.VolumeMount{}, false
}

func pvcClaimName(volumes []v1.Volume, name string) string {
	for _, volume := range volumes {
		if volume.Name != name || volume.PersistentVolumeClaim == nil {
			continue
		}
		return volume.PersistentVolumeClaim.ClaimName
	}
	return ""
}

func envVarMap(vars []v1.EnvVar) map[string]string {
	out := make(map[string]string, len(vars))
	for _, envVar := range vars {
		out[envVar.Name] = envVar.Value
	}
	return out
}

func (h *Handler) workloadFactStore() workloadfacts.Store {
	if h.options.WorkloadFactStore != nil {
		return h.options.WorkloadFactStore
	}
	return workloadfacts.NewConfigMapStore(h.k8sClient.Clientset().CoreV1().ConfigMaps(h.k8sClient.Namespace()))
}

func (h *Handler) getScopedWorkloadPod(ctx context.Context, scope workloadScope) (*v1.Pod, error) {
	pod, err := h.k8sClient.GetPod(ctx, scope.podName())
	if err != nil {
		return nil, err
	}
	if err := scope.validatePod(pod); err != nil {
		return nil, err
	}
	return pod, nil
}

func (h *Handler) loadWorkloadFact(ctx context.Context, store workloadfacts.Store, workspaceID, projectID, workloadID string) (workloadfacts.Fact, bool, error) {
	fact, err := store.Get(ctx, workloadfacts.Key{WorkspaceID: workspaceID, ProjectID: projectID, WorkloadID: workloadID})
	if err != nil {
		if stderrors.Is(err, workloadfacts.ErrNotFound) {
			return workloadfacts.Fact{}, false, nil
		}
		return workloadfacts.Fact{}, false, err
	}
	return fact, true, nil
}

func (h *Handler) recordWorkloadFact(ctx context.Context, workspaceID, projectID, workloadID, bindingID string, pod *v1.Pod) error {
	fact, err := h.workloadFactFromPod(workspaceID, projectID, workloadID, bindingID, pod)
	if err != nil {
		return err
	}
	return h.workloadFactStore().Save(ctx, fact)
}

func (h *Handler) rollbackCreatedPodAfterFactFailure(ctx context.Context, workloadID, podName string) {
	podName = strings.TrimSpace(podName)
	if podName == "" {
		return
	}
	rollbackCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 15*time.Second)
	defer cancel()
	if err := h.k8sClient.DeletePod(rollbackCtx, podName, 0); err != nil {
		log.Printf("workload/%s: pod rollback after workload fact write failure failed: %s", workloadID, observability.RedactLogValue(err))
	}
}

func (h *Handler) workloadFactFromPod(workspaceID, projectID, workloadID, bindingID string, pod *v1.Pod) (workloadfacts.Fact, error) {
	if pod == nil {
		return workloadfacts.Fact{}, fmt.Errorf("pod is required")
	}
	namespaceID := ""
	mountBindingID := strings.TrimSpace(bindingID)
	if ref, ok := workloadMountRefFromPod(pod); ok {
		namespaceID = ref.namespaceID
		mountBindingID = ref.mountBindingID
	}
	if h.options.AFSCPClient != nil && (namespaceID == "" || mountBindingID == "") {
		return workloadfacts.Fact{}, fmt.Errorf("pod is missing AFSCP workload mount annotations")
	}
	return workloadfacts.Fact{
		WorkspaceID:        workspaceID,
		ProjectID:          projectID,
		WorkloadID:         workloadID,
		WorkspaceBindingID: mountBindingID,
		NamespaceID:        namespaceID,
		MountBindingID:     mountBindingID,
		PodName:            pod.GetName(),
		PodUID:             string(pod.GetUID()),
	}, nil
}

func (h *Handler) releaseWorkloadMountFromFact(ctx context.Context, r *http.Request, fact workloadfacts.Fact) error {
	if h.options.AFSCPClient == nil {
		return nil
	}
	ref, ok := workloadMountRefFromFact(fact)
	if !ok {
		return fmt.Errorf("workload terminal fact is missing AFSCP mount ref")
	}
	correlationID := requestCorrelationID(r)
	_, err := h.options.AFSCPClient.ReleaseWorkloadMountBinding(ctx, ref.namespaceID, ref.mountBindingID, correlationID, factLifecycleIdempotencyKey("release", fact))
	return err
}

func (h *Handler) markWorkloadMountReleasedFromFact(ctx context.Context, r *http.Request, fact workloadfacts.Fact) error {
	if h.options.AFSCPClient == nil {
		return nil
	}
	ref, ok := workloadMountRefFromFact(fact)
	if !ok {
		return fmt.Errorf("workload terminal fact is missing AFSCP mount ref")
	}
	correlationID := requestCorrelationID(r)
	_, err := h.options.AFSCPClient.UpdateWorkloadMountStatus(ctx, ref.namespaceID, ref.mountBindingID, "released", "workload pod deleted", time.Now().UTC(), correlationID, factLifecycleIdempotencyKey("status-released", fact))
	return err
}

type workloadMountRef struct {
	namespaceID    string
	mountBindingID string
}

func (h *Handler) heartbeatWorkloadMount(ctx context.Context, r *http.Request, pod *v1.Pod) error {
	if h.options.AFSCPClient == nil {
		return nil
	}
	ref, ok := workloadMountRefFromPod(pod)
	if !ok {
		return fmt.Errorf("pod is missing AFSCP workload mount annotations")
	}
	correlationID := requestCorrelationID(r)
	_, err := h.options.AFSCPClient.HeartbeatWorkloadMountBinding(ctx, ref.namespaceID, ref.mountBindingID, correlationID, idempotencyKey("heartbeat", pod.Name, correlationID))
	return err
}

func (h *Handler) releaseWorkloadMount(ctx context.Context, r *http.Request, pod *v1.Pod) error {
	if h.options.AFSCPClient == nil {
		return nil
	}
	ref, ok := workloadMountRefFromPod(pod)
	if !ok {
		return fmt.Errorf("pod is missing AFSCP workload mount annotations")
	}
	correlationID := requestCorrelationID(r)
	if _, err := h.options.AFSCPClient.ReleaseWorkloadMountBinding(ctx, ref.namespaceID, ref.mountBindingID, correlationID, podLifecycleIdempotencyKey("release", pod)); err != nil {
		return err
	}
	return nil
}

func (h *Handler) flushWorkloadStorage(ctx context.Context, pod *v1.Pod) error {
	if h.options.AFSCPClient == nil {
		return nil
	}
	if _, ok := workloadMountRefFromPod(pod); !ok {
		return fmt.Errorf("pod is missing AFSCP workload mount annotations")
	}
	mountPath, ok := workloadMountPathFromPod(pod)
	if !ok {
		return fmt.Errorf("pod is missing AFSCP mount path annotation")
	}
	barrier := h.options.StorageFlushBarrier
	if barrier == nil {
		barrier = podExecStorageFlushBarrier{executor: h.executor}
	}
	return barrier.FlushWorkloadMount(ctx, pod, mountPath)
}

func (h *Handler) markWorkloadMountReleased(ctx context.Context, r *http.Request, pod *v1.Pod) error {
	if h.options.AFSCPClient == nil {
		return nil
	}
	ref, ok := workloadMountRefFromPod(pod)
	if !ok {
		return fmt.Errorf("pod is missing AFSCP workload mount annotations")
	}
	correlationID := requestCorrelationID(r)
	_, err := h.options.AFSCPClient.UpdateWorkloadMountStatus(ctx, ref.namespaceID, ref.mountBindingID, "released", "workload pod deleted", time.Now().UTC(), correlationID, podLifecycleIdempotencyKey("status-released", pod))
	return err
}

func workloadMountRefFromPod(pod *v1.Pod) (workloadMountRef, bool) {
	if pod == nil || pod.Annotations == nil {
		return workloadMountRef{}, false
	}
	namespaceID := strings.TrimSpace(pod.Annotations["mbos.io/afscp-namespace-id"])
	mountBindingID := strings.TrimSpace(pod.Annotations["mbos.io/afscp-mount-binding-id"])
	if namespaceID == "" || mountBindingID == "" {
		return workloadMountRef{}, false
	}
	return workloadMountRef{namespaceID: namespaceID, mountBindingID: mountBindingID}, true
}

func workloadMountRefFromFact(fact workloadfacts.Fact) (workloadMountRef, bool) {
	namespaceID := strings.TrimSpace(fact.NamespaceID)
	mountBindingID := strings.TrimSpace(fact.MountBindingID)
	if namespaceID == "" || mountBindingID == "" {
		return workloadMountRef{}, false
	}
	return workloadMountRef{namespaceID: namespaceID, mountBindingID: mountBindingID}, true
}

func workloadMountPathFromPod(pod *v1.Pod) (string, bool) {
	if pod == nil || pod.Annotations == nil {
		return "", false
	}
	mountPath := strings.TrimSpace(pod.Annotations["mbos.io/mount-path"])
	if mountPath == "" || !path.IsAbs(mountPath) || mountPath == "/" || containsParentPathSegment(mountPath) || path.Clean(mountPath) != mountPath {
		return "", false
	}
	if strings.Contains(mountPath, "\\") {
		return "", false
	}
	return mountPath, true
}

type podExecStorageFlushBarrier struct {
	executor *k8s.Executor
	timeout  time.Duration
}

func (barrier podExecStorageFlushBarrier) FlushWorkloadMount(ctx context.Context, pod *v1.Pod, mountPath string) error {
	if barrier.executor == nil {
		return fmt.Errorf("k8s executor is not configured")
	}
	if pod == nil {
		return fmt.Errorf("pod is required")
	}
	podName := strings.TrimSpace(pod.GetName())
	if podName == "" {
		return fmt.Errorf("pod name is required")
	}
	timeout := barrier.timeout
	if timeout <= 0 {
		timeout = 30 * time.Second
	}
	result, err := barrier.executor.Exec(ctx, podName, &k8s.ExecOptions{
		Command: []string{
			"sh",
			"-c",
			`if [ ! -d "$1" ]; then echo "mount path not found: $1" >&2; exit 1; fi; sync -f "$1" 2>/dev/null || sync`,
			"sh",
			mountPath,
		},
		Container: "main",
		Timeout:   timeout,
	})
	if err != nil {
		return err
	}
	if result == nil {
		return fmt.Errorf("storage flush command returned no result")
	}
	if result.ExitCode != 0 {
		stderr := strings.TrimSpace(result.Stderr)
		if stderr == "" {
			stderr = "no stderr"
		}
		return fmt.Errorf("storage flush command exited %d: %s", result.ExitCode, stderr)
	}
	return nil
}

func requestCorrelationID(r *http.Request) string {
	return observability.RequestCorrelationID(r, "workload")
}

func idempotencyKey(action, podName, correlationID string) string {
	return sanitizeIdempotencyKey("workload", action, podName, correlationID)
}

func podLifecycleIdempotencyKey(action string, pod *v1.Pod) string {
	name := ""
	uid := ""
	if pod != nil {
		name = pod.GetName()
		uid = string(pod.UID)
	}
	return sanitizeIdempotencyKey("workload", action, name, uid)
}

func factLifecycleIdempotencyKey(action string, fact workloadfacts.Fact) string {
	return sanitizeIdempotencyKey("workload", action, fact.PodName, fact.PodUID)
}

func logWorkloadMountDependencyFailure(r *http.Request, message string, err error, workspaceID, projectID, workloadID string, pod *v1.Pod) {
	ref, _ := workloadMountRefFromPod(pod)
	podName := ""
	if pod != nil {
		podName = pod.GetName()
	}
	log.Printf("workload/%s: %s: workspace=%s project=%s workload=%s pod=%s namespace_id=%s mount_binding_id=%s request_id=%s correlation_id=%s error=%s",
		workloadID,
		message,
		workspaceID,
		projectID,
		workloadID,
		podName,
		ref.namespaceID,
		ref.mountBindingID,
		observability.GetRequestID(r),
		requestCorrelationID(r),
		observability.RedactLogValue(err),
	)
}

func logWorkloadFactDependencyFailure(r *http.Request, message string, err error, workspaceID, projectID, workloadID string, fact workloadfacts.Fact) {
	ref, _ := workloadMountRefFromFact(fact)
	log.Printf("workload/%s: %s: workspace=%s project=%s workload=%s pod=%s namespace_id=%s mount_binding_id=%s request_id=%s correlation_id=%s error=%s",
		workloadID,
		message,
		workspaceID,
		projectID,
		workloadID,
		fact.PodName,
		ref.namespaceID,
		ref.mountBindingID,
		observability.GetRequestID(r),
		requestCorrelationID(r),
		observability.RedactLogValue(err),
	)
}

func sanitizeIdempotencyKey(parts ...string) string {
	compactParts := make([]string, 0, len(parts))
	for _, part := range parts {
		if trimmed := strings.TrimSpace(part); trimmed != "" {
			compactParts = append(compactParts, trimmed)
		}
	}
	value := strings.Join(compactParts, "-")
	value = strings.ToLower(value)
	value = strings.Map(func(r rune) rune {
		switch {
		case r >= 'a' && r <= 'z':
			return r
		case r >= '0' && r <= '9':
			return r
		case r == '-' || r == '_':
			return r
		default:
			return '-'
		}
	}, value)
	if len(value) > 180 {
		return value[:180]
	}
	return value
}

func boolString(value bool) string {
	if value {
		return "true"
	}
	return "false"
}

func (h *Handler) waitForPodDeletion(ctx context.Context, podName string, timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	var lastErr error
	for time.Now().Before(deadline) {
		exists, err := h.k8sClient.PodExists(ctx, podName)
		if err != nil {
			lastErr = err
			time.Sleep(1 * time.Second)
			continue
		}
		if !exists {
			return nil
		}
		time.Sleep(1 * time.Second)
	}
	if lastErr != nil {
		return lastErr
	}
	return fmt.Errorf("pod %s still exists", podName)
}

func workloadStatusForPod(pod *v1.Pod) string {
	if pod == nil {
		return "offline"
	}
	switch pod.Status.Phase {
	case v1.PodRunning:
		return "running"
	case v1.PodFailed:
		return "failed"
	case v1.PodSucceeded:
		return "offline"
	default:
		return "pending"
	}
}

func podPhaseString(pod *v1.Pod) string {
	if pod == nil {
		return "offline"
	}
	if pod.Status.Phase == "" {
		return string(v1.PodPending)
	}
	return string(pod.Status.Phase)
}

// PodName returns the scope-qualified pod name for a workload.
func PodName(workspaceID, projectID, workloadID string) string {
	return newWorkloadScope(workspaceID, projectID, workloadID).podName()
}

func boolPtr(b bool) *bool {
	return &b
}

func jsonResponse(w http.ResponseWriter, status int, v interface{}) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(v)
}

func jsonError(w http.ResponseWriter, r *http.Request, status int, code string, message string) {
	httperror.Write(w, r, status, code, message)
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
