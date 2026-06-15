package workspacebinding

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net/http"
	"path"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/agentsmith-project/agentsmith-sandbox-control-plane/internal/afscp"
	"github.com/agentsmith-project/agentsmith-sandbox-control-plane/internal/httperror"
	"github.com/agentsmith-project/agentsmith-sandbox-control-plane/internal/observability"
	"github.com/agentsmith-project/agentsmith-sandbox-control-plane/internal/workloadfacts"
	v1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
)

type Handler struct {
	k8sClient k8sClient
	options   Options
}

type k8sClient interface {
	EnsurePersistentVolume(ctx context.Context, volume *v1.PersistentVolume) error
	GetPersistentVolume(ctx context.Context, name string) (*v1.PersistentVolume, error)
	DeletePersistentVolume(ctx context.Context, name string) error
	EnsurePersistentVolumeClaim(ctx context.Context, namespace string, claim *v1.PersistentVolumeClaim) error
	GetPersistentVolumeClaim(ctx context.Context, namespace, name string) (*v1.PersistentVolumeClaim, error)
	DeletePersistentVolumeClaim(ctx context.Context, namespace, name string) error
	ListPods(ctx context.Context, namespace string, opts metav1.ListOptions) (*v1.PodList, error)
}

type afscpPlanClient interface {
	GetOrchestratorMountPlan(ctx context.Context, namespaceID, mountBindingID, correlationID string) (afscp.OrchestratorMountPlan, error)
	UpdateWorkloadMountStatus(ctx context.Context, namespaceID, mountBindingID, status, reason string, observedAt time.Time, correlationID, idempotencyKey string) (afscp.OperationEnvelope, error)
}

type Options struct {
	Namespace        string
	CSIDriver        string
	StorageCapacity  string
	StorageClassName string
	AFSCPClient      afscpPlanClient
	WorkloadFacts    workloadfacts.Source
	ReleaseFacts     WorkspaceBindingReleaseStore
}

const errorCodeWorkspaceBindingReleaseIncomplete = "workspace_binding_release_incomplete"
const workspaceBindingReleaseRetryAfter = "1"

type EnsureRequest struct {
	NamespaceID    string `json:"namespace_id"`
	MountBindingID string `json:"mount_binding_id,omitempty"`
}

type BindingStatus struct {
	BindingID           string `json:"binding_id"`
	WorkspaceID         string `json:"workspace_id"`
	ProjectID           string `json:"project_id"`
	Status              string `json:"status"`
	Namespace           string `json:"namespace"`
	PVName              string `json:"pv_name"`
	PVCName             string `json:"pvc_name"`
	VolumeHandle        string `json:"volume_handle"`
	NamespaceID         string `json:"namespace_id"`
	MountBindingID      string `json:"mount_binding_id"`
	VolumeID            string `json:"volume_id"`
	MountPath           string `json:"mount_path"`
	ReadOnly            bool   `json:"read_only"`
	PayloadVolumeSubdir string `json:"-"`
	StorageClassName    string `json:"storage_class_name,omitempty"`
	CreatedAt           string `json:"created_at,omitempty"`
	UpdatedAt           string `json:"updated_at,omitempty"`
}

type ResolvedMount struct {
	PVCName             string
	NamespaceID         string
	MountBindingID      string
	VolumeID            string
	MountPath           string
	ReadOnly            bool
	PayloadVolumeSubdir string
	SecurityPolicy      afscp.SecurityPolicy
}

const (
	labelManagedBy                     = "app.kubernetes.io/managed-by"
	labelManagedByOwner                = "agentsmith"
	annotationWorkspaceID              = "mbos.io/workspace-id"
	annotationProjectID                = "mbos.io/project-id"
	annotationCreatedAt                = "mbos.io/created-at"
	annotationUpdatedAt                = "mbos.io/updated-at"
	annotationVolumeHandle             = "mbos.io/volume-handle"
	annotationAFSCPNamespaceID         = "mbos.io/afscp-namespace-id"
	annotationAFSCPMountBindingID      = "mbos.io/afscp-mount-binding-id"
	annotationAFSCPVolumeID            = "mbos.io/afscp-volume-id"
	annotationPayloadVolumeSubdir      = "mbos.io/payload-volume-subdir"
	annotationMountPath                = "mbos.io/mount-path"
	annotationReadOnly                 = "mbos.io/read-only"
	annotationRunAsNonRoot             = "mbos.io/run-as-non-root"
	annotationAllowPrivileged          = "mbos.io/allow-privileged"
	annotationJVSControlOutsidePayload = "mbos.io/jvs-control-outside-payload"
	juiceFSMountOptionAttrCache        = "attr-cache=0s"
	juiceFSMountOptionEntryCache       = "entry-cache=0s"
	juiceFSMountOptionDirEntryCache    = "dir-entry-cache=0s"
	juiceFSMountOptionNegativeCache    = "negative-entry-cache=0s"
	ensurePVCBoundTimeout              = time.Second
	ensurePVCBoundPollInterval         = 50 * time.Millisecond
	ensurePVCBoundRetryAfter           = "1"
	workspaceBindingDeleteTimeout      = time.Second
	workspaceBindingDeletePollInterval = 50 * time.Millisecond
)

func NewHandler(k8sClient k8sClient, options Options) *Handler {
	return &Handler{k8sClient: k8sClient, options: options}
}

func (h *Handler) RegisterRoutes(mux *http.ServeMux) {
	mux.HandleFunc("/v1/workspaces/", h.routeRequest)
}

func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	h.routeRequest(w, r)
}

func (h *Handler) routeRequest(w http.ResponseWriter, r *http.Request) {
	workspaceID, projectID, bindingID, ok := parseBindingRoute(r.URL.Path)
	if !ok {
		jsonError(w, r, http.StatusNotFound, "not_found", "not found")
		return
	}
	if !isValidName(bindingID) {
		jsonError(w, r, http.StatusBadRequest, "invalid_request", "invalid binding_id")
		return
	}
	switch r.Method {
	case http.MethodPut:
		h.handleEnsure(w, r, workspaceID, projectID, bindingID)
	case http.MethodGet:
		h.handleGet(w, r, workspaceID, projectID, bindingID)
	case http.MethodDelete:
		h.handleDelete(w, r, workspaceID, projectID, bindingID)
	default:
		jsonError(w, r, http.StatusMethodNotAllowed, "method_not_allowed", "method not allowed")
	}
}

func parseBindingRoute(path string) (workspaceID, projectID, bindingID string, ok bool) {
	const prefix = "/v1/workspaces/"
	if !strings.HasPrefix(path, prefix) {
		return
	}
	parts := strings.Split(strings.TrimPrefix(path, prefix), "/")
	if len(parts) != 5 {
		return
	}
	if parts[1] != "projects" || parts[3] != "workspace-bindings" {
		return
	}
	workspaceID = parts[0]
	projectID = parts[2]
	bindingID = parts[4]
	ok = workspaceID != "" && projectID != "" && bindingID != ""
	return
}

func (h *Handler) handleEnsure(w http.ResponseWriter, r *http.Request, workspaceID, projectID, bindingID string) {
	req, err := decodeEnsureRequest(r)
	if err != nil {
		jsonError(w, r, http.StatusBadRequest, "invalid_request", "invalid request body: "+err.Error())
		return
	}
	if h.options.AFSCPClient == nil {
		jsonError(w, r, http.StatusInternalServerError, "internal_error", "AFSCP client is not configured")
		return
	}
	mountBindingID := firstNonEmpty(req.MountBindingID, bindingID)
	if err := validateAFSCPNamespaceID(req.NamespaceID); err != nil {
		jsonError(w, r, http.StatusBadRequest, "invalid_request", "invalid namespace_id")
		return
	}
	if err := validateAFSCPMountBindingID(mountBindingID); err != nil {
		jsonError(w, r, http.StatusBadRequest, "invalid_request", "invalid mount_binding_id")
		return
	}
	if req.MountBindingID != "" && req.MountBindingID != bindingID {
		jsonError(w, r, http.StatusBadRequest, "invalid_request", "binding_id and mount_binding_id must match")
		return
	}
	correlationID := observability.RequestCorrelationID(r, "asbcp")
	plan, err := h.options.AFSCPClient.GetOrchestratorMountPlan(r.Context(), req.NamespaceID, mountBindingID, correlationID)
	if err != nil {
		log.Printf("workspace_binding/%s: AFSCP orchestrator mount plan is unavailable: workspace=%s project=%s namespace_id=%s mount_binding_id=%s request_id=%s correlation_id=%s error=%s",
			bindingID,
			workspaceID,
			projectID,
			req.NamespaceID,
			mountBindingID,
			observability.GetRequestID(r),
			correlationID,
			observability.RedactLogValue(err),
		)
		jsonError(w, r, http.StatusBadGateway, "dependency_failure", "AFSCP orchestrator mount plan is unavailable")
		return
	}
	if err := validatePlan(req.NamespaceID, mountBindingID, plan); err != nil {
		log.Printf("workspace_binding/%s: AFSCP orchestrator mount plan is invalid: workspace=%s project=%s namespace_id=%s mount_binding_id=%s request_id=%s correlation_id=%s error=%s",
			bindingID,
			workspaceID,
			projectID,
			req.NamespaceID,
			mountBindingID,
			observability.GetRequestID(r),
			correlationID,
			observability.RedactLogValue(err),
		)
		jsonError(w, r, http.StatusBadGateway, "dependency_failure", "AFSCP orchestrator mount plan is invalid")
		return
	}

	ctx := r.Context()
	now := time.Now().UTC().Format(time.RFC3339)
	pv, pvc := names(workspaceID, projectID, bindingID)
	status := BindingStatus{
		BindingID:           bindingID,
		WorkspaceID:         workspaceID,
		ProjectID:           projectID,
		Status:              "ready",
		Namespace:           h.options.Namespace,
		PVName:              pv,
		PVCName:             pvc,
		VolumeHandle:        volumeHandle(workspaceID, projectID, bindingID),
		NamespaceID:         req.NamespaceID,
		MountBindingID:      plan.MountBindingID,
		VolumeID:            plan.VolumeID,
		MountPath:           plan.MountPath,
		ReadOnly:            plan.ReadOnly,
		PayloadVolumeSubdir: plan.PayloadVolumeSubdir,
		StorageClassName:    h.options.StorageClassName,
		CreatedAt:           now,
		UpdatedAt:           now,
	}

	builtPV := h.buildPV(status, plan)
	if err := h.k8sClient.EnsurePersistentVolume(ctx, builtPV); err != nil {
		jsonError(w, r, http.StatusInternalServerError, "internal_error", "ensure persistent volume failed")
		return
	}
	if err := h.k8sClient.EnsurePersistentVolumeClaim(ctx, h.options.Namespace, h.buildPVC(status, plan)); err != nil {
		jsonError(w, r, http.StatusInternalServerError, "internal_error", "ensure persistent volume claim failed")
		return
	}
	readyPVC, err := h.waitForPVCBound(ctx, h.options.Namespace, pvc)
	if err != nil && errors.Is(err, errPVCBoundNotReady) {
		w.Header().Set("Retry-After", ensurePVCBoundRetryAfter)
		reason, phase := pvcReasonPhaseFromError(err)
		jsonErrorWithDetails(w, r, http.StatusServiceUnavailable, "not_ready", "workspace binding is not ready: "+err.Error(), bindingReadinessDetails("workspace_binding.ensure", workspaceID, projectID, bindingID, "persistent_volume_claim", reason, phase, ensurePVCBoundRetryAfter))
		return
	}
	if err != nil {
		jsonError(w, r, http.StatusInternalServerError, "internal_error", err.Error())
		return
	}
	status, err = bindingStatusFromObjects(workspaceID, projectID, bindingID, h.options.Namespace, builtPV, readyPVC)
	if err != nil {
		jsonError(w, r, http.StatusInternalServerError, "internal_error", "workspace binding status is invalid")
		return
	}
	jsonResponse(w, http.StatusOK, status)
}

var errPVCBoundNotReady = errors.New("persistent volume claim is not bound")

type pvcBoundNotReadyError struct {
	err error
}

func (e pvcBoundNotReadyError) Error() string {
	return e.err.Error()
}

func (e pvcBoundNotReadyError) Unwrap() error {
	return errPVCBoundNotReady
}

func (h *Handler) waitForPVCBound(ctx context.Context, namespace, name string) (*v1.PersistentVolumeClaim, error) {
	ctx, cancel := context.WithTimeout(ctx, ensurePVCBoundTimeout)
	defer cancel()

	ticker := time.NewTicker(ensurePVCBoundPollInterval)
	defer ticker.Stop()

	var lastErr error
	for {
		pvc, err := h.k8sClient.GetPersistentVolumeClaim(ctx, namespace, name)
		if err != nil {
			if apierrors.IsNotFound(err) {
				lastErr = fmt.Errorf("persistent volume claim %q is not visible yet", name)
			} else {
				isContextDoneError := errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded)
				if ctx.Err() != nil && lastErr != nil && isContextDoneError {
					return nil, pvcBoundNotReadyError{err: lastErr}
				}
				return nil, errors.New("get persistent volume claim failed")
			}
		} else {
			if err := RequirePVCBound(pvc); err == nil {
				return pvc, nil
			} else {
				lastErr = err
			}
		}

		select {
		case <-ctx.Done():
			if lastErr != nil {
				return nil, pvcBoundNotReadyError{err: lastErr}
			}
			return nil, pvcBoundNotReadyError{err: ctx.Err()}
		case <-ticker.C:
		}
	}
}

func (h *Handler) handleGet(w http.ResponseWriter, r *http.Request, workspaceID, projectID, bindingID string) {
	ctx := r.Context()
	pvName, pvcName := names(workspaceID, projectID, bindingID)
	pv, err := h.k8sClient.GetPersistentVolume(ctx, pvName)
	if err != nil {
		if apierrors.IsNotFound(err) {
			jsonError(w, r, http.StatusNotFound, "not_found", "workspace binding not found")
			return
		}
		jsonError(w, r, http.StatusInternalServerError, "internal_error", "get persistent volume failed")
		return
	}
	pvc, err := h.k8sClient.GetPersistentVolumeClaim(ctx, h.options.Namespace, pvcName)
	if err != nil {
		if apierrors.IsNotFound(err) {
			message := fmt.Sprintf("workspace binding is not ready: persistent volume claim %q is not visible yet", pvcName)
			w.Header().Set("Retry-After", ensurePVCBoundRetryAfter)
			jsonErrorWithDetails(w, r, http.StatusServiceUnavailable, "not_ready", message, bindingReadinessDetails("workspace_binding.get", workspaceID, projectID, bindingID, "persistent_volume_claim", "pvc_missing", "missing", ensurePVCBoundRetryAfter))
			return
		}
		jsonError(w, r, http.StatusInternalServerError, "internal_error", "get persistent volume claim failed")
		return
	}
	if err := RequirePVCBound(pvc); err != nil {
		w.Header().Set("Retry-After", ensurePVCBoundRetryAfter)
		reason, phase := pvcReasonPhaseFromObject(pvc, err)
		jsonErrorWithDetails(w, r, http.StatusServiceUnavailable, "not_ready", "workspace binding is not ready: "+err.Error(), bindingReadinessDetails("workspace_binding.get", workspaceID, projectID, bindingID, "persistent_volume_claim", reason, phase, ensurePVCBoundRetryAfter))
		return
	}
	status, err := bindingStatusFromObjects(workspaceID, projectID, bindingID, h.options.Namespace, pv, pvc)
	if err != nil {
		jsonError(w, r, http.StatusInternalServerError, "internal_error", "workspace binding status is invalid")
		return
	}
	jsonResponse(w, http.StatusOK, status)
}

func (h *Handler) handleDelete(w http.ResponseWriter, r *http.Request, workspaceID, projectID, bindingID string) {
	ctx := r.Context()
	pvName, pvcName := names(workspaceID, projectID, bindingID)
	active, err := h.activeWorkloadsForBinding(ctx, workspaceID, projectID, bindingID)
	if err != nil {
		log.Printf("workspacebinding/%s: release truth check unavailable: %s", bindingID, observability.RedactLogValue(err))
		jsonError(w, r, http.StatusConflict, errorCodeWorkspaceBindingReleaseIncomplete, "workspace binding release truth is unavailable; retry after workload release state is known")
		return
	}
	if len(active) > 0 {
		jsonError(w, r, http.StatusConflict, errorCodeWorkspaceBindingReleaseIncomplete, "workspace binding release is incomplete; delete workloads first: "+strings.Join(active, ","))
		return
	}
	releaseFact, hasReleaseFact := WorkspaceBindingReleaseFact{}, false
	if h.options.AFSCPClient != nil {
		var err error
		releaseFact, err = h.ensureWorkspaceBindingReleaseFact(ctx, workspaceID, projectID, bindingID, pvName, pvcName)
		if err != nil {
			log.Printf("workspacebinding/%s: AFSCP mount reference unavailable before release: workspace=%s project=%s request_id=%s correlation_id=%s error=%s",
				bindingID,
				workspaceID,
				projectID,
				observability.GetRequestID(r),
				observability.RequestCorrelationID(r, "asbcp"),
				observability.RedactLogValue(err),
			)
			if errors.Is(err, errWorkspaceBindingReleaseFactStoreUnavailable) || errors.Is(err, errWorkspaceBindingReleaseFactWriteFailed) {
				w.Header().Set("Retry-After", workspaceBindingReleaseRetryAfter)
				jsonErrorWithDetails(w, r, http.StatusConflict, errorCodeWorkspaceBindingReleaseIncomplete, "workspace binding release truth is unavailable; retry after workspace binding release state is known", bindingReleaseDetails("workspace_binding.delete", workspaceID, projectID, bindingID, "workspace_binding_release_fact", "release_fact_unavailable", "unknown", "release_incomplete", errorCodeWorkspaceBindingReleaseIncomplete, workspaceBindingReleaseRetryAfter))
				return
			}
			jsonErrorWithDetails(w, r, http.StatusConflict, errorCodeWorkspaceBindingReleaseIncomplete, "workspace binding release truth is unavailable; retry after workspace binding state is known", bindingReleaseDetails("workspace_binding.delete", workspaceID, projectID, bindingID, "afscp_workload_mount_binding", "mount_ref_unavailable", "unknown", "release_incomplete", errorCodeWorkspaceBindingReleaseIncomplete, workspaceBindingReleaseRetryAfter))
			return
		}
		hasReleaseFact = true
	}
	if err := h.k8sClient.DeletePersistentVolumeClaim(ctx, h.options.Namespace, pvcName); err != nil {
		jsonError(w, r, http.StatusInternalServerError, "internal_error", "delete persistent volume claim failed")
		return
	}
	if err := h.k8sClient.DeletePersistentVolume(ctx, pvName); err != nil {
		jsonError(w, r, http.StatusInternalServerError, "internal_error", "delete persistent volume failed")
		return
	}
	if err := h.waitForWorkspaceBindingStorageDeleted(ctx, h.options.Namespace, pvName, pvcName); err != nil {
		if details, ok := storageDeletionPendingBindingReleaseDetails(err, "workspace_binding.delete", workspaceID, projectID, bindingID); ok {
			w.Header().Set("Retry-After", workspaceBindingReleaseRetryAfter)
			jsonErrorWithDetails(w, r, http.StatusConflict, errorCodeWorkspaceBindingReleaseIncomplete, "workspace binding storage deletion is pending", details)
			return
		}
		log.Printf("workspacebinding/%s: storage deletion boundary check failed: workspace=%s project=%s request_id=%s correlation_id=%s error=%s",
			bindingID,
			workspaceID,
			projectID,
			observability.GetRequestID(r),
			observability.RequestCorrelationID(r, "asbcp"),
			observability.RedactLogValue(err),
		)
		jsonError(w, r, http.StatusInternalServerError, "internal_error", "workspace binding storage deletion check failed")
		return
	}
	if hasReleaseFact && !releaseFact.StorageDeleted {
		releaseFact.StorageDeleted = true
		if err := h.saveWorkspaceBindingReleaseFact(ctx, releaseFact); err != nil {
			log.Printf("workspacebinding/%s: release fact storage-deleted write failed: workspace=%s project=%s request_id=%s correlation_id=%s error=%s",
				bindingID,
				workspaceID,
				projectID,
				observability.GetRequestID(r),
				observability.RequestCorrelationID(r, "asbcp"),
				observability.RedactLogValue(err),
			)
			jsonError(w, r, http.StatusInternalServerError, "internal_error", "workspace binding release fact write failed")
			return
		}
	}
	if hasReleaseFact && releaseFact.TerminalStatusDone {
		jsonResponse(w, http.StatusOK, map[string]string{"message": "binding deleted"})
		return
	}
	if hasReleaseFact {
		ref := releaseFact.MountRef()
		if err := h.markWorkspaceBindingMountReleased(ctx, r, workspaceID, projectID, bindingID, releaseFact); err != nil {
			log.Printf("workspacebinding/%s: AFSCP workspace binding released status failed: workspace=%s project=%s namespace_id=%s mount_binding_id=%s request_id=%s correlation_id=%s error=%s",
				bindingID,
				workspaceID,
				projectID,
				ref.namespaceID,
				ref.mountBindingID,
				observability.GetRequestID(r),
				observability.RequestCorrelationID(r, "asbcp"),
				observability.RedactLogValue(err),
			)
			if details, ok := afscpPendingBindingReleaseDetails(err, "workspace_binding.delete", workspaceID, projectID, bindingID, "released_status_pending"); ok {
				w.Header().Set("Retry-After", workspaceBindingReleaseRetryAfter)
				jsonErrorWithDetails(w, r, http.StatusConflict, errorCodeWorkspaceBindingReleaseIncomplete, "AFSCP workspace binding released status is pending", details)
				return
			}
			jsonError(w, r, http.StatusBadGateway, "dependency_failure", "AFSCP workspace binding released status failed")
			return
		}
		releaseFact.TerminalStatusDone = true
		if err := h.saveWorkspaceBindingReleaseFact(ctx, releaseFact); err != nil {
			log.Printf("workspacebinding/%s: release fact terminal-status write failed: workspace=%s project=%s request_id=%s correlation_id=%s error=%s",
				bindingID,
				workspaceID,
				projectID,
				observability.GetRequestID(r),
				observability.RequestCorrelationID(r, "asbcp"),
				observability.RedactLogValue(err),
			)
			jsonError(w, r, http.StatusInternalServerError, "internal_error", "workspace binding release fact write failed")
			return
		}
	}
	jsonResponse(w, http.StatusOK, map[string]string{"message": "binding deleted"})
}

type workspaceBindingMountRef struct {
	namespaceID    string
	mountBindingID string
}

var (
	errWorkspaceBindingReleaseFactStoreUnavailable = errors.New("workspace binding release fact store unavailable")
	errWorkspaceBindingReleaseFactWriteFailed      = errors.New("workspace binding release fact write failed")
	errWorkspaceBindingStorageDeletionPending      = errors.New("workspace binding storage deletion pending")
)

type workspaceBindingStorageDeletionPendingError struct {
	phase string
}

func (e workspaceBindingStorageDeletionPendingError) Error() string {
	if strings.TrimSpace(e.phase) == "" {
		return errWorkspaceBindingStorageDeletionPending.Error()
	}
	return errWorkspaceBindingStorageDeletionPending.Error() + ": " + e.phase
}

func (e workspaceBindingStorageDeletionPendingError) Unwrap() error {
	return errWorkspaceBindingStorageDeletionPending
}

func (h *Handler) ensureWorkspaceBindingReleaseFact(ctx context.Context, workspaceID, projectID, bindingID, pvName, pvcName string) (WorkspaceBindingReleaseFact, error) {
	store, err := h.workspaceBindingReleaseFactStore()
	if err != nil {
		return WorkspaceBindingReleaseFact{}, err
	}
	key := WorkspaceBindingReleaseKey{WorkspaceID: workspaceID, ProjectID: projectID, BindingID: bindingID}
	fact, err := store.Get(ctx, key)
	if err == nil {
		if err := validateWorkspaceBindingReleaseFact(fact, workspaceID, projectID, bindingID); err != nil {
			return WorkspaceBindingReleaseFact{}, err
		}
		return fact, nil
	}
	if !errors.Is(err, errWorkspaceBindingReleaseFactNotFound) {
		return WorkspaceBindingReleaseFact{}, fmt.Errorf("%w: %v", errWorkspaceBindingReleaseFactStoreUnavailable, err)
	}

	ref, err := h.resolveWorkspaceBindingMountRef(ctx, workspaceID, projectID, bindingID, pvName, pvcName)
	if err != nil {
		return WorkspaceBindingReleaseFact{}, err
	}
	fact = WorkspaceBindingReleaseFact{
		WorkspaceID:    workspaceID,
		ProjectID:      projectID,
		BindingID:      bindingID,
		PVName:         pvName,
		PVCName:        pvcName,
		NamespaceID:    ref.namespaceID,
		MountBindingID: ref.mountBindingID,
	}
	normalizeWorkspaceBindingReleaseFact(&fact)
	if err := validateWorkspaceBindingReleaseFact(fact, workspaceID, projectID, bindingID); err != nil {
		return WorkspaceBindingReleaseFact{}, err
	}
	if err := store.Save(ctx, fact); err != nil {
		return WorkspaceBindingReleaseFact{}, fmt.Errorf("%w: %v", errWorkspaceBindingReleaseFactWriteFailed, err)
	}
	return fact, nil
}

func (h *Handler) saveWorkspaceBindingReleaseFact(ctx context.Context, fact WorkspaceBindingReleaseFact) error {
	store, err := h.workspaceBindingReleaseFactStore()
	if err != nil {
		return err
	}
	normalizeWorkspaceBindingReleaseFact(&fact)
	if err := validateWorkspaceBindingReleaseFact(fact, fact.WorkspaceID, fact.ProjectID, fact.BindingID); err != nil {
		return err
	}
	if err := store.Save(ctx, fact); err != nil {
		return fmt.Errorf("%w: %v", errWorkspaceBindingReleaseFactWriteFailed, err)
	}
	return nil
}

func (h *Handler) workspaceBindingReleaseFactStore() (WorkspaceBindingReleaseStore, error) {
	if h.options.ReleaseFacts != nil {
		return h.options.ReleaseFacts, nil
	}
	provider, ok := h.k8sClient.(configMapClientProvider)
	if !ok {
		return nil, errWorkspaceBindingReleaseFactStoreUnavailable
	}
	return newWorkspaceBindingReleaseConfigMapStore(provider.Clientset().CoreV1().ConfigMaps(h.options.Namespace)), nil
}

func validateWorkspaceBindingReleaseFact(fact WorkspaceBindingReleaseFact, workspaceID, projectID, bindingID string) error {
	normalizeWorkspaceBindingReleaseFact(&fact)
	if fact.WorkspaceID != strings.TrimSpace(workspaceID) || fact.ProjectID != strings.TrimSpace(projectID) || fact.BindingID != strings.TrimSpace(bindingID) {
		return fmt.Errorf("workspace binding release fact scope mismatch")
	}
	if err := validateAFSCPNamespaceID(fact.NamespaceID); err != nil {
		return fmt.Errorf("invalid release fact namespace_id")
	}
	if err := validateAFSCPMountBindingID(fact.MountBindingID); err != nil {
		return fmt.Errorf("invalid release fact mount_binding_id")
	}
	if fact.MountBindingID != strings.TrimSpace(bindingID) {
		return fmt.Errorf("release fact mount_binding_id does not match request")
	}
	if strings.TrimSpace(fact.PVName) == "" || strings.TrimSpace(fact.PVCName) == "" {
		return fmt.Errorf("release fact storage object names are missing")
	}
	return nil
}

func (h *Handler) resolveWorkspaceBindingMountRef(ctx context.Context, workspaceID, projectID, bindingID, pvName, pvcName string) (workspaceBindingMountRef, error) {
	if h.options.AFSCPClient == nil {
		return workspaceBindingMountRef{}, nil
	}
	pvc, err := h.k8sClient.GetPersistentVolumeClaim(ctx, h.options.Namespace, pvcName)
	if err == nil {
		return workspaceBindingMountRefFromAnnotations(bindingID, pvc.GetAnnotations())
	}
	if err != nil && !apierrors.IsNotFound(err) {
		return workspaceBindingMountRef{}, errors.New("get persistent volume claim failed")
	}

	pv, err := h.k8sClient.GetPersistentVolume(ctx, pvName)
	if err == nil {
		return workspaceBindingMountRefFromAnnotations(bindingID, pv.GetAnnotations())
	}
	if err != nil && !apierrors.IsNotFound(err) {
		return workspaceBindingMountRef{}, errors.New("get persistent volume failed")
	}
	return workspaceBindingMountRef{}, errors.New("workspace binding resources are missing")
}

func workspaceBindingMountRefFromAnnotations(bindingID string, annotations map[string]string) (workspaceBindingMountRef, error) {
	ref := workspaceBindingMountRef{
		namespaceID:    strings.TrimSpace(annotations[annotationAFSCPNamespaceID]),
		mountBindingID: strings.TrimSpace(annotations[annotationAFSCPMountBindingID]),
	}
	if err := validateAFSCPNamespaceID(ref.namespaceID); err != nil {
		return workspaceBindingMountRef{}, fmt.Errorf("invalid binding namespace_id annotation")
	}
	if err := validateAFSCPMountBindingID(ref.mountBindingID); err != nil {
		return workspaceBindingMountRef{}, fmt.Errorf("invalid binding mount_binding_id annotation")
	}
	if ref.mountBindingID != strings.TrimSpace(bindingID) {
		return workspaceBindingMountRef{}, fmt.Errorf("binding mount_binding_id annotation does not match request")
	}
	return ref, nil
}

func (h *Handler) waitForWorkspaceBindingStorageDeleted(ctx context.Context, namespace, pvName, pvcName string) error {
	waitCtx, cancel := context.WithTimeout(ctx, workspaceBindingDeleteTimeout)
	defer cancel()

	ticker := time.NewTicker(workspaceBindingDeletePollInterval)
	defer ticker.Stop()

	lastPhase := "unknown"
	for {
		pvcGone, pvcPhase, err := h.persistentVolumeClaimDeleted(waitCtx, namespace, pvcName)
		if err != nil {
			return err
		}
		pvGone, pvPhase, err := h.persistentVolumeDeleted(waitCtx, pvName)
		if err != nil {
			return err
		}
		if pvcGone && pvGone {
			return nil
		}
		lastPhase = storageDeletionPhase(pvcGone, pvcPhase, pvGone, pvPhase)

		select {
		case <-waitCtx.Done():
			return workspaceBindingStorageDeletionPendingError{phase: lastPhase}
		case <-ticker.C:
		}
	}
}

func (h *Handler) persistentVolumeClaimDeleted(ctx context.Context, namespace, name string) (bool, string, error) {
	pvc, err := h.k8sClient.GetPersistentVolumeClaim(ctx, namespace, name)
	if err != nil {
		if apierrors.IsNotFound(err) {
			return true, "missing", nil
		}
		return false, "", errors.New("get persistent volume claim failed")
	}
	if pvc == nil {
		return true, "missing", nil
	}
	if pvc.GetDeletionTimestamp() != nil {
		return false, "pvc_deleting", nil
	}
	return false, "pvc_present", nil
}

func (h *Handler) persistentVolumeDeleted(ctx context.Context, name string) (bool, string, error) {
	pv, err := h.k8sClient.GetPersistentVolume(ctx, name)
	if err != nil {
		if apierrors.IsNotFound(err) {
			return true, "missing", nil
		}
		return false, "", errors.New("get persistent volume failed")
	}
	if pv == nil {
		return true, "missing", nil
	}
	if pv.GetDeletionTimestamp() != nil {
		return false, "pv_deleting", nil
	}
	return false, "pv_present", nil
}

func storageDeletionPhase(pvcGone bool, pvcPhase string, pvGone bool, pvPhase string) string {
	switch {
	case !pvcGone && !pvGone:
		return pvcPhase + "+" + pvPhase
	case !pvcGone:
		return pvcPhase
	case !pvGone:
		return pvPhase
	default:
		return "deleted"
	}
}

func (h *Handler) markWorkspaceBindingMountReleased(ctx context.Context, r *http.Request, workspaceID, projectID, bindingID string, fact WorkspaceBindingReleaseFact) error {
	if h.options.AFSCPClient == nil {
		return nil
	}
	correlationID := observability.RequestCorrelationID(r, "asbcp")
	ref := fact.MountRef()
	_, err := h.options.AFSCPClient.UpdateWorkloadMountStatus(ctx, ref.namespaceID, ref.mountBindingID, "released", "workspace binding deleted", fact.ObservedAt.UTC(), correlationID, workspaceBindingStatusIdempotencyKey(workspaceID, projectID, bindingID, ref))
	return err
}

func workspaceBindingStatusIdempotencyKey(workspaceID, projectID, bindingID string, ref workspaceBindingMountRef) string {
	return workloadfacts.ObjectName("workspace-binding-status-released", workspaceID, projectID, bindingID, ref.namespaceID, ref.mountBindingID)
}

func (h *Handler) activeWorkloadsForBinding(ctx context.Context, workspaceID, projectID, bindingID string) ([]string, error) {
	source, err := h.workloadFactSource()
	if err != nil {
		return nil, err
	}
	facts, err := source.ListByBinding(ctx, workspaceID, projectID, bindingID)
	if err != nil {
		return nil, err
	}
	active := make(map[string]struct{})
	for _, fact := range facts {
		if fact.Terminal() {
			continue
		}
		addActiveWorkloadName(active, fact.WorkloadID, fact.PodName)
	}

	pods, err := h.k8sClient.ListPods(ctx, h.options.Namespace, metav1.ListOptions{})
	if err != nil {
		return nil, err
	}
	for _, pod := range pods.Items {
		if name, ok := livePodWorkloadNameForBinding(pod, workspaceID, projectID, bindingID); ok {
			addActiveWorkloadName(active, name, pod.Name)
		}
	}

	out := make([]string, 0, len(active))
	for name := range active {
		out = append(out, name)
	}
	sort.Strings(out)
	return out, nil
}

func addActiveWorkloadName(active map[string]struct{}, values ...string) {
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value != "" {
			active[value] = struct{}{}
			return
		}
	}
}

func livePodWorkloadNameForBinding(pod v1.Pod, workspaceID, projectID, bindingID string) (string, bool) {
	annotations := pod.GetAnnotations()
	labels := pod.GetLabels()
	if podReferencesPVCClaim(pod, PVCName(workspaceID, projectID, bindingID)) {
		return livePodWorkloadName(pod), true
	}
	if strings.TrimSpace(annotations[annotationAFSCPMountBindingID]) != bindingID {
		return "", false
	}
	if !podScopeMatches(annotations[annotationWorkspaceID], labels["workspace_id"], workspaceID) {
		return "", false
	}
	if !podScopeMatches(annotations[annotationProjectID], labels["project_id"], projectID) {
		return "", false
	}
	name := livePodWorkloadName(pod)
	return name, name != ""
}

func livePodWorkloadName(pod v1.Pod) string {
	name := strings.TrimSpace(pod.GetAnnotations()["mbos.io/workload-id"])
	if name == "" {
		name = pod.GetName()
	}
	return name
}

func podReferencesPVCClaim(pod v1.Pod, pvcName string) bool {
	pvcName = strings.TrimSpace(pvcName)
	if pvcName == "" {
		return false
	}
	for _, volume := range pod.Spec.Volumes {
		if volume.PersistentVolumeClaim == nil {
			continue
		}
		if strings.TrimSpace(volume.PersistentVolumeClaim.ClaimName) == pvcName {
			return true
		}
	}
	return false
}

func podScopeMatches(annotationValue, labelValue, want string) bool {
	annotationValue = strings.TrimSpace(annotationValue)
	labelValue = strings.TrimSpace(labelValue)
	want = strings.TrimSpace(want)
	if annotationValue != "" {
		return annotationValue == want
	}
	return labelValue == want || labelValue == workloadfacts.LabelValue(want)
}

type configMapClientProvider interface {
	Clientset() *kubernetes.Clientset
}

func (h *Handler) workloadFactSource() (workloadfacts.Source, error) {
	if h.options.WorkloadFacts != nil {
		return h.options.WorkloadFacts, nil
	}
	provider, ok := h.k8sClient.(configMapClientProvider)
	if !ok {
		return nil, errors.New("workload fact source is not configured")
	}
	return workloadfacts.NewConfigMapStore(provider.Clientset().CoreV1().ConfigMaps(h.options.Namespace)), nil
}

func (h *Handler) buildPV(status BindingStatus, plan afscp.OrchestratorMountPlan) *v1.PersistentVolume {
	capacity := firstNonEmpty(h.options.StorageCapacity, "1Pi")
	return &v1.PersistentVolume{
		ObjectMeta: metav1.ObjectMeta{
			Name: status.PVName,
			Labels: map[string]string{
				labelManagedBy:             labelManagedByOwner,
				"mbos.io/mount-binding-id": workloadfacts.LabelValue(status.MountBindingID),
			},
			Annotations: map[string]string{
				annotationWorkspaceID:              status.WorkspaceID,
				annotationProjectID:                status.ProjectID,
				annotationVolumeHandle:             status.VolumeHandle,
				annotationAFSCPNamespaceID:         status.NamespaceID,
				annotationAFSCPMountBindingID:      status.MountBindingID,
				annotationAFSCPVolumeID:            status.VolumeID,
				annotationPayloadVolumeSubdir:      status.PayloadVolumeSubdir,
				annotationMountPath:                status.MountPath,
				annotationReadOnly:                 boolString(status.ReadOnly),
				annotationRunAsNonRoot:             boolString(plan.SecurityPolicy.RunAsNonRoot),
				annotationAllowPrivileged:          boolString(plan.SecurityPolicy.AllowPrivileged),
				annotationJVSControlOutsidePayload: boolString(plan.SecurityPolicy.JVSControlOutsidePayload),
				annotationCreatedAt:                status.CreatedAt,
				annotationUpdatedAt:                status.UpdatedAt,
			},
		},
		Spec: v1.PersistentVolumeSpec{
			Capacity:                      v1.ResourceList{v1.ResourceStorage: resource.MustParse(capacity)},
			AccessModes:                   []v1.PersistentVolumeAccessMode{v1.ReadWriteMany},
			PersistentVolumeReclaimPolicy: v1.PersistentVolumeReclaimRetain,
			VolumeMode:                    func() *v1.PersistentVolumeMode { m := v1.PersistentVolumeFilesystem; return &m }(),
			StorageClassName:              status.StorageClassName,
			MountOptions:                  juiceFSMountOptions(plan.PayloadVolumeSubdir),
			PersistentVolumeSource: v1.PersistentVolumeSource{
				CSI: &v1.CSIPersistentVolumeSource{
					Driver:       firstNonEmpty(h.options.CSIDriver, "csi.juicefs.com"),
					VolumeHandle: status.VolumeHandle,
					FSType:       "juicefs",
					NodePublishSecretRef: &v1.SecretReference{
						Name:      plan.SecretRef.Name,
						Namespace: plan.SecretRef.Namespace,
					},
				},
			},
		},
	}
}

func (h *Handler) buildPVC(status BindingStatus, plan afscp.OrchestratorMountPlan) *v1.PersistentVolumeClaim {
	capacity := firstNonEmpty(h.options.StorageCapacity, "1Pi")
	return &v1.PersistentVolumeClaim{
		ObjectMeta: metav1.ObjectMeta{
			Name:      status.PVCName,
			Namespace: h.options.Namespace,
			Labels: map[string]string{
				labelManagedBy:             labelManagedByOwner,
				"mbos.io/mount-binding-id": workloadfacts.LabelValue(status.MountBindingID),
			},
			Annotations: map[string]string{
				annotationWorkspaceID:              status.WorkspaceID,
				annotationProjectID:                status.ProjectID,
				annotationVolumeHandle:             status.VolumeHandle,
				annotationAFSCPNamespaceID:         status.NamespaceID,
				annotationAFSCPMountBindingID:      status.MountBindingID,
				annotationAFSCPVolumeID:            status.VolumeID,
				annotationPayloadVolumeSubdir:      status.PayloadVolumeSubdir,
				annotationMountPath:                status.MountPath,
				annotationReadOnly:                 boolString(status.ReadOnly),
				annotationRunAsNonRoot:             boolString(plan.SecurityPolicy.RunAsNonRoot),
				annotationAllowPrivileged:          boolString(plan.SecurityPolicy.AllowPrivileged),
				annotationJVSControlOutsidePayload: boolString(plan.SecurityPolicy.JVSControlOutsidePayload),
				annotationCreatedAt:                status.CreatedAt,
				annotationUpdatedAt:                status.UpdatedAt,
			},
		},
		Spec: v1.PersistentVolumeClaimSpec{
			AccessModes: []v1.PersistentVolumeAccessMode{v1.ReadWriteMany},
			StorageClassName: func() *string {
				value := status.StorageClassName
				return &value
			}(),
			VolumeName: status.PVName,
			Resources: v1.VolumeResourceRequirements{
				Requests: v1.ResourceList{v1.ResourceStorage: resource.MustParse(capacity)},
			},
		},
	}
}

var validNameRegex = regexp.MustCompile(`^[A-Za-z0-9_-]{1,63}$`)
var validNamespaceIDRegex = regexp.MustCompile(`^ns_[A-Za-z0-9][A-Za-z0-9_-]{1,62}$`)
var validMountBindingIDRegex = regexp.MustCompile(`^wmb_[A-Za-z0-9][A-Za-z0-9_-]{1,62}$`)
var validVolumeIDRegex = regexp.MustCompile(`^vol_[A-Za-z0-9][A-Za-z0-9_-]{1,62}$`)

func isValidName(value string) bool {
	return validNameRegex.MatchString(value)
}

func sanitizeK8sName(value string, fallback string) string {
	normalized := strings.ToLower(value)
	normalized = regexp.MustCompile(`[^a-z0-9-]+`).ReplaceAllString(normalized, "-")
	normalized = regexp.MustCompile(`-{2,}`).ReplaceAllString(normalized, "-")
	normalized = strings.Trim(normalized, "-")
	if len(normalized) > 63 {
		normalized = strings.Trim(normalized[:63], "-")
	}
	if normalized == "" {
		return fallback
	}
	return normalized
}

func names(workspaceID, projectID, bindingID string) (pvName, pvcName string) {
	return workloadfacts.ObjectName("juicefs-pv", workspaceID, projectID, bindingID),
		workloadfacts.ObjectName("juicefs-pvc", workspaceID, projectID, bindingID)
}

func PVCName(workspaceID, projectID, bindingID string) string {
	_, pvcName := names(workspaceID, projectID, bindingID)
	return pvcName
}

func PVName(workspaceID, projectID, bindingID string) string {
	pvName, _ := names(workspaceID, projectID, bindingID)
	return pvName
}

func volumeHandle(workspaceID, projectID, bindingID string) string {
	return workloadfacts.ObjectName("juicefs", workspaceID, projectID, bindingID)
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return strings.TrimSpace(value)
		}
	}
	return ""
}

func annotation[T interface{ GetAnnotations() map[string]string }](obj T, key string) string {
	return obj.GetAnnotations()[key]
}

func derefString(value *string) string {
	if value == nil {
		return ""
	}
	return *value
}

func jsonResponse(w http.ResponseWriter, status int, payload any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(payload)
}

func jsonError(w http.ResponseWriter, r *http.Request, status int, code string, message string) {
	httperror.Write(w, r, status, code, message)
}

func jsonErrorWithDetails(w http.ResponseWriter, r *http.Request, status int, code string, message string, details map[string]string) {
	httperror.WriteWithDetails(w, r, status, code, message, details)
}

func bindingReadinessDetails(operation, workspaceID, projectID, bindingID, resource, reason, phase, retryAfter string) map[string]string {
	details := map[string]string{
		"operation":    operation,
		"workspace_id": workspaceID,
		"project_id":   projectID,
		"binding_id":   bindingID,
		"resource":     resource,
		"reason":       reason,
		"phase":        phase,
		"status":       "not_ready",
		"stable_code":  "not_ready",
	}
	if strings.TrimSpace(retryAfter) != "" {
		details["retry_after"] = retryAfter
	}
	return details
}

func bindingReleaseDetails(operation, workspaceID, projectID, bindingID, resource, reason, phase, status, stableCode, retryAfter string) map[string]string {
	details := map[string]string{
		"operation":    operation,
		"workspace_id": workspaceID,
		"project_id":   projectID,
		"binding_id":   bindingID,
		"resource":     resource,
		"reason":       reason,
		"phase":        phase,
		"status":       status,
		"stable_code":  stableCode,
	}
	if strings.TrimSpace(retryAfter) != "" {
		details["retry_after"] = retryAfter
	}
	return details
}

func afscpPendingBindingReleaseDetails(err error, operation, workspaceID, projectID, bindingID, status string) (map[string]string, bool) {
	var pending *afscp.PendingOperationError
	if !errors.As(err, &pending) {
		return nil, false
	}
	phase := strings.TrimSpace(pending.OperationState)
	if phase == "" {
		phase = "unknown"
	}
	details := bindingReleaseDetails(operation, workspaceID, projectID, bindingID, "afscp_workload_mount_binding", "afscp_operation_pending", phase, status, errorCodeWorkspaceBindingReleaseIncomplete, workspaceBindingReleaseRetryAfter)
	if operationID := strings.TrimSpace(pending.OperationID); operationID != "" {
		details["dependency_operation_id"] = operationID
	}
	if requestID := strings.TrimSpace(pending.RequestID); requestID != "" {
		details["dependency_request_id"] = requestID
	}
	if code := strings.TrimSpace(pending.Code); code != "" {
		details["dependency_code"] = code
	}
	details["dependency_state"] = phase
	return details, true
}

func storageDeletionPendingBindingReleaseDetails(err error, operation, workspaceID, projectID, bindingID string) (map[string]string, bool) {
	var pending workspaceBindingStorageDeletionPendingError
	if !errors.As(err, &pending) {
		return nil, false
	}
	phase := strings.TrimSpace(pending.phase)
	if phase == "" {
		phase = "unknown"
	}
	return bindingReleaseDetails(operation, workspaceID, projectID, bindingID, "persistent_volume_binding", "storage_deletion_pending", phase, "storage_deletion_pending", errorCodeWorkspaceBindingReleaseIncomplete, workspaceBindingReleaseRetryAfter), true
}

func pvcReasonPhaseFromError(err error) (string, string) {
	message := err.Error()
	if strings.Contains(message, "not visible yet") {
		return "pvc_missing", "missing"
	}
	if strings.Contains(message, "Pending") {
		return "pvc_unbound", string(v1.ClaimPending)
	}
	if strings.Contains(message, "Bound but has no volumeName") {
		return "pvc_unbound", string(v1.ClaimBound)
	}
	return "pvc_unbound", "unknown"
}

func pvcReasonPhaseFromObject(pvc *v1.PersistentVolumeClaim, err error) (string, string) {
	phase := "unknown"
	if pvc != nil && pvc.Status.Phase != "" {
		phase = string(pvc.Status.Phase)
	}
	reason := "pvc_unbound"
	if err != nil && strings.Contains(err.Error(), "not visible yet") {
		reason = "pvc_missing"
		phase = "missing"
	}
	return reason, phase
}

func decodeEnsureRequest(r *http.Request) (EnsureRequest, error) {
	var req EnsureRequest
	decoder := json.NewDecoder(r.Body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&req); err != nil {
		return req, err
	}
	return req, nil
}

func validatePlan(namespaceID, mountBindingID string, plan afscp.OrchestratorMountPlan) error {
	if plan.MountBindingID != mountBindingID {
		return fmt.Errorf("mount binding id mismatch")
	}
	if err := validateAFSCPNamespaceID(namespaceID); err != nil {
		return err
	}
	if err := validateAFSCPMountBindingID(plan.MountBindingID); err != nil {
		return err
	}
	if !validVolumeIDRegex.MatchString(plan.VolumeID) {
		return fmt.Errorf("invalid volume id")
	}
	if err := validateMountPath(plan.MountPath); err != nil {
		return err
	}
	if err := validatePayloadVolumeSubdir(plan.PayloadVolumeSubdir); err != nil {
		return err
	}
	if err := validateSecretRef(plan.SecretRef); err != nil {
		return err
	}
	if !plan.SecurityPolicy.RunAsNonRoot || !plan.SecurityPolicy.JVSControlOutsidePayload {
		return fmt.Errorf("security policy does not satisfy sandbox workload requirements")
	}
	if plan.SecurityPolicy.AllowPrivileged {
		return fmt.Errorf("privileged workload mounts are not supported")
	}
	return nil
}

func validateAFSCPNamespaceID(value string) error {
	if !validNamespaceIDRegex.MatchString(strings.TrimSpace(value)) {
		return fmt.Errorf("invalid namespace id")
	}
	return nil
}

func validateAFSCPMountBindingID(value string) error {
	if !validMountBindingIDRegex.MatchString(strings.TrimSpace(value)) {
		return fmt.Errorf("invalid mount binding id")
	}
	return nil
}

func validateMountPath(value string) error {
	if value == "" || strings.TrimSpace(value) != value || !path.IsAbs(value) || value == "/" {
		return fmt.Errorf("mount_path must be a non-root absolute container path")
	}
	if strings.Contains(value, "\\") {
		return fmt.Errorf("mount_path must not contain backslashes")
	}
	for _, part := range strings.Split(value, "/")[1:] {
		if part == "" || part == "." || part == ".." {
			return fmt.Errorf("mount_path must be clean")
		}
	}
	cleaned := path.Clean(value)
	if cleaned != value {
		return fmt.Errorf("mount_path must be clean")
	}
	for _, reserved := range []string{"/proc", "/sys", "/dev", "/run/secrets", "/var/run/secrets"} {
		if value == reserved || strings.HasPrefix(value, reserved+"/") {
			return fmt.Errorf("mount_path uses a runtime reserved path")
		}
	}
	return nil
}

func validatePayloadVolumeSubdir(value string) error {
	if value == "" || strings.TrimSpace(value) != value || strings.HasPrefix(value, "/") || strings.Contains(value, "\\") {
		return fmt.Errorf("payload_volume_subdir must be relative")
	}
	if !strings.HasPrefix(value, "afscp/") || !strings.HasSuffix(value, "/payload") {
		return fmt.Errorf("payload_volume_subdir must use afscp payload root")
	}
	for _, part := range strings.Split(value, "/") {
		if part == "" || part == "." || part == ".." {
			return fmt.Errorf("payload_volume_subdir must be clean")
		}
	}
	return nil
}

func validateSecretRef(ref afscp.SecretRef) error {
	if !validDNSLabel(ref.Namespace, 63) || !validDNSSubdomain(ref.Name, 253) {
		return fmt.Errorf("invalid secret_ref")
	}
	return nil
}

func validDNSSubdomain(value string, maxLen int) bool {
	if strings.TrimSpace(value) == "" || len(value) > maxLen {
		return false
	}
	for _, part := range strings.Split(value, ".") {
		if !validDNSLabel(part, 63) {
			return false
		}
	}
	return true
}

func validDNSLabel(value string, maxLen int) bool {
	if value == "" || len(value) > maxLen {
		return false
	}
	for idx, r := range value {
		switch {
		case r >= 'a' && r <= 'z':
		case r >= '0' && r <= '9':
		case r == '-' && idx > 0 && idx < len(value)-1:
		default:
			return false
		}
	}
	return true
}

func bindingStatusFromObjects(workspaceID, projectID, bindingID, namespace string, pv *v1.PersistentVolume, pvc *v1.PersistentVolumeClaim) (BindingStatus, error) {
	resolved, err := ResolvedMountFromPVC(pvc)
	if err != nil {
		return BindingStatus{}, err
	}
	pvName, pvcName := names(workspaceID, projectID, bindingID)
	if err := validatePVPayloadMountOptions(pv, resolved.PayloadVolumeSubdir); err != nil {
		return BindingStatus{}, err
	}
	if pv != nil && pv.Spec.CSI != nil && pv.Spec.CSI.VolumeAttributes != nil {
		if subdir := pv.Spec.CSI.VolumeAttributes["subdir"]; subdir != "" && subdir != resolved.PayloadVolumeSubdir {
			return BindingStatus{}, fmt.Errorf("pv payload subdir does not match pvc annotation")
		}
	}
	return BindingStatus{
		BindingID:           bindingID,
		WorkspaceID:         workspaceID,
		ProjectID:           projectID,
		Status:              "ready",
		Namespace:           namespace,
		PVName:              pvName,
		PVCName:             pvcName,
		VolumeHandle:        firstNonEmpty(annotation(pvc, annotationVolumeHandle), annotation(pv, annotationVolumeHandle), volumeHandle(workspaceID, projectID, bindingID)),
		NamespaceID:         resolved.NamespaceID,
		MountBindingID:      resolved.MountBindingID,
		VolumeID:            resolved.VolumeID,
		MountPath:           resolved.MountPath,
		ReadOnly:            resolved.ReadOnly,
		PayloadVolumeSubdir: resolved.PayloadVolumeSubdir,
		StorageClassName:    derefString(pvc.Spec.StorageClassName),
		CreatedAt:           annotation(pvc, annotationCreatedAt),
		UpdatedAt:           annotation(pvc, annotationUpdatedAt),
	}, nil
}

func ResolvedMountFromPVC(pvc *v1.PersistentVolumeClaim) (ResolvedMount, error) {
	if pvc == nil {
		return ResolvedMount{}, errors.New("workspace binding pvc is required")
	}
	annotations := pvc.GetAnnotations()
	resolved := ResolvedMount{
		PVCName:             pvc.GetName(),
		NamespaceID:         annotations[annotationAFSCPNamespaceID],
		MountBindingID:      annotations[annotationAFSCPMountBindingID],
		VolumeID:            annotations[annotationAFSCPVolumeID],
		MountPath:           annotations[annotationMountPath],
		ReadOnly:            annotations[annotationReadOnly] == "true",
		PayloadVolumeSubdir: annotations[annotationPayloadVolumeSubdir],
		SecurityPolicy: afscp.SecurityPolicy{
			RunAsNonRoot:             annotations[annotationRunAsNonRoot] == "true",
			AllowPrivileged:          annotations[annotationAllowPrivileged] == "true",
			JVSControlOutsidePayload: annotations[annotationJVSControlOutsidePayload] == "true",
		},
	}
	if err := validateAFSCPNamespaceID(resolved.NamespaceID); err != nil {
		return ResolvedMount{}, fmt.Errorf("invalid binding namespace_id annotation")
	}
	if err := validateAFSCPMountBindingID(resolved.MountBindingID); err != nil {
		return ResolvedMount{}, fmt.Errorf("invalid binding mount_binding_id annotation")
	}
	if !validVolumeIDRegex.MatchString(resolved.VolumeID) {
		return ResolvedMount{}, fmt.Errorf("invalid binding volume_id annotation")
	}
	if err := validateMountPath(resolved.MountPath); err != nil {
		return ResolvedMount{}, err
	}
	if err := validatePayloadVolumeSubdir(resolved.PayloadVolumeSubdir); err != nil {
		return ResolvedMount{}, err
	}
	if !resolved.SecurityPolicy.RunAsNonRoot || resolved.SecurityPolicy.AllowPrivileged || !resolved.SecurityPolicy.JVSControlOutsidePayload {
		return ResolvedMount{}, fmt.Errorf("binding security policy is not allowed")
	}
	return resolved, nil
}

func RequirePVCBound(pvc *v1.PersistentVolumeClaim) error {
	if pvc == nil {
		return errors.New("workspace binding pvc is required")
	}
	phase := pvc.Status.Phase
	if phase != v1.ClaimBound {
		if phase == "" {
			return fmt.Errorf("persistent volume claim %q is not Bound", pvc.GetName())
		}
		return fmt.Errorf("persistent volume claim %q is %s, not Bound", pvc.GetName(), phase)
	}
	if strings.TrimSpace(pvc.Spec.VolumeName) == "" {
		return fmt.Errorf("persistent volume claim %q is Bound but has no volumeName", pvc.GetName())
	}
	return nil
}

func payloadSubdirMountOption(payloadVolumeSubdir string) string {
	return "subdir=" + strings.TrimSpace(payloadVolumeSubdir)
}

func juiceFSMountOptions(payloadVolumeSubdir string) []string {
	return []string{
		payloadSubdirMountOption(payloadVolumeSubdir),
		juiceFSMountOptionAttrCache,
		juiceFSMountOptionEntryCache,
		juiceFSMountOptionDirEntryCache,
		juiceFSMountOptionNegativeCache,
	}
}

func validatePVPayloadMountOptions(pv *v1.PersistentVolume, payloadVolumeSubdir string) error {
	if pv == nil {
		return errors.New("workspace binding pv is required")
	}
	want := payloadSubdirMountOption(payloadVolumeSubdir)
	found := false
	for _, option := range pv.Spec.MountOptions {
		trimmed := strings.TrimSpace(option)
		if !strings.HasPrefix(trimmed, "subdir=") {
			continue
		}
		if trimmed != want {
			return fmt.Errorf("pv payload subdir mount option does not match pvc annotation")
		}
		if found {
			return fmt.Errorf("pv payload subdir mount option is duplicated")
		}
		found = true
	}
	if !found {
		return fmt.Errorf("pv payload subdir mount option is missing")
	}
	return nil
}

func boolString(value bool) string {
	if value {
		return "true"
	}
	return "false"
}
