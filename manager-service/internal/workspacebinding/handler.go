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
}

type Options struct {
	Namespace        string
	CSIDriver        string
	StorageCapacity  string
	StorageClassName string
	AFSCPClient      afscpPlanClient
	WorkloadFacts    workloadfacts.Source
}

const errorCodeWorkspaceBindingReleaseIncomplete = "workspace_binding_release_incomplete"

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

	if err := h.k8sClient.EnsurePersistentVolume(ctx, h.buildPV(status, plan)); err != nil {
		jsonError(w, r, http.StatusInternalServerError, "internal_error", "ensure persistent volume failed")
		return
	}
	if err := h.k8sClient.EnsurePersistentVolumeClaim(ctx, h.options.Namespace, h.buildPVC(status, plan)); err != nil {
		jsonError(w, r, http.StatusInternalServerError, "internal_error", "ensure persistent volume claim failed")
		return
	}
	jsonResponse(w, http.StatusOK, status)
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
			jsonError(w, r, http.StatusNotFound, "not_found", "workspace binding not found")
			return
		}
		jsonError(w, r, http.StatusInternalServerError, "internal_error", "get persistent volume claim failed")
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
	if err := h.k8sClient.DeletePersistentVolumeClaim(ctx, h.options.Namespace, pvcName); err != nil {
		jsonError(w, r, http.StatusInternalServerError, "internal_error", "delete persistent volume claim failed")
		return
	}
	if err := h.k8sClient.DeletePersistentVolume(ctx, pvName); err != nil {
		jsonError(w, r, http.StatusInternalServerError, "internal_error", "delete persistent volume failed")
		return
	}
	jsonResponse(w, http.StatusOK, map[string]string{"message": "binding deleted"})
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
			MountOptions:                  []string{payloadSubdirMountOption(plan.PayloadVolumeSubdir)},
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

func payloadSubdirMountOption(payloadVolumeSubdir string) string {
	return "subdir=" + strings.TrimSpace(payloadVolumeSubdir)
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
