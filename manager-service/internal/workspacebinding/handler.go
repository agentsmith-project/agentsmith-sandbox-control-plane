package workspacebinding

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"regexp"
	"strings"
	"time"

	v1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

type Handler struct {
	k8sClient k8sClient
	options   Options
}

type k8sClient interface {
	EnsureSecret(ctx context.Context, namespace string, secret *v1.Secret) error
	GetSecret(ctx context.Context, namespace, name string) (*v1.Secret, error)
	DeleteSecret(ctx context.Context, namespace, name string) error
	EnsurePersistentVolume(ctx context.Context, volume *v1.PersistentVolume) error
	GetPersistentVolume(ctx context.Context, name string) (*v1.PersistentVolume, error)
	DeletePersistentVolume(ctx context.Context, name string) error
	EnsurePersistentVolumeClaim(ctx context.Context, namespace string, claim *v1.PersistentVolumeClaim) error
	GetPersistentVolumeClaim(ctx context.Context, namespace, name string) (*v1.PersistentVolumeClaim, error)
	DeletePersistentVolumeClaim(ctx context.Context, namespace, name string) error
}

type Options struct {
	Namespace             string
	CSIDriver             string
	StorageCapacity       string
	StorageClassName      string
	MountOptions          []string
	Subdir                string
	MountServiceAccount   string
	MountImage            string
	StorageEndpoint       string
	StorageAccessKey      string
	StorageSecretKey      string
}

type EnsureRequest struct {
	FileLibraryID       string   `json:"file_library_id"`
	FilesystemName      string   `json:"filesystem_name"`
	MetadataURL         string   `json:"metadata_url"`
	StorageEndpoint     string   `json:"storage_endpoint,omitempty"`
	StorageCapacity     string   `json:"storage_capacity,omitempty"`
	StorageClassName    string   `json:"storage_class_name,omitempty"`
	MountOptions        []string `json:"mount_options,omitempty"`
	Subdir              string   `json:"subdir,omitempty"`
	MountServiceAccount string   `json:"mount_service_account,omitempty"`
	MountImage          string   `json:"mount_image,omitempty"`
}

type BindingStatus struct {
	BindingID        string   `json:"binding_id"`
	WorkspaceID      string   `json:"workspace_id"`
	ProjectID        string   `json:"project_id"`
	FileLibraryID    string   `json:"file_library_id"`
	Status           string   `json:"status"`
	Namespace        string   `json:"namespace"`
	SecretName       string   `json:"secret_name"`
	PVName           string   `json:"pv_name"`
	PVCName          string   `json:"pvc_name"`
	VolumeHandle     string   `json:"volume_handle"`
	FilesystemName   string   `json:"filesystem_name"`
	MountPath        string   `json:"mount_path"`
	StorageClassName string   `json:"storage_class_name,omitempty"`
	MountOptions     []string `json:"mount_options,omitempty"`
	Subdir           string   `json:"subdir,omitempty"`
	CreatedAt        string   `json:"created_at,omitempty"`
	UpdatedAt        string   `json:"updated_at,omitempty"`
}

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
		return
	}
	if !isValidName(bindingID) {
		jsonError(w, http.StatusBadRequest, "invalid binding_id")
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
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
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
	var req EnsureRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		jsonError(w, http.StatusBadRequest, "invalid request body: "+err.Error())
		return
	}
	if req.FileLibraryID == "" || req.FilesystemName == "" || req.MetadataURL == "" {
		jsonError(w, http.StatusBadRequest, "file_library_id, filesystem_name, metadata_url are required")
		return
	}
	if req.FileLibraryID != bindingID {
		jsonError(w, http.StatusBadRequest, "binding_id and file_library_id must match")
		return
	}
	if firstNonEmpty(req.StorageEndpoint, h.options.StorageEndpoint) == "" {
		jsonError(w, http.StatusInternalServerError, "storage endpoint is not configured")
		return
	}
	if strings.TrimSpace(h.options.StorageAccessKey) == "" || strings.TrimSpace(h.options.StorageSecretKey) == "" {
		jsonError(w, http.StatusInternalServerError, "storage access key and secret key must be configured")
		return
	}

	ctx := r.Context()
	now := time.Now().UTC().Format(time.RFC3339)
	secret, pv, pvc := names(workspaceID, projectID, bindingID)
	status := BindingStatus{
		BindingID:        bindingID,
		WorkspaceID:      workspaceID,
		ProjectID:        projectID,
		FileLibraryID:    req.FileLibraryID,
		Status:           "ready",
		Namespace:        h.options.Namespace,
		SecretName:       secret,
		PVName:           pv,
		PVCName:          pvc,
		VolumeHandle:     volumeHandle(workspaceID, projectID, bindingID),
		FilesystemName:   req.FilesystemName,
		MountPath:        "/workspace",
		StorageClassName: firstNonEmpty(req.StorageClassName, h.options.StorageClassName),
		MountOptions:     firstSlice(req.MountOptions, h.options.MountOptions),
		Subdir:           firstNonEmpty(req.Subdir, h.options.Subdir),
		CreatedAt:        now,
		UpdatedAt:        now,
	}

	if err := h.k8sClient.EnsureSecret(ctx, h.options.Namespace, h.buildSecret(status, req)); err != nil {
		jsonError(w, http.StatusInternalServerError, "ensure secret failed: "+err.Error())
		return
	}
	if err := h.k8sClient.EnsurePersistentVolume(ctx, h.buildPV(status, req)); err != nil {
		jsonError(w, http.StatusInternalServerError, "ensure persistent volume failed: "+err.Error())
		return
	}
	if err := h.k8sClient.EnsurePersistentVolumeClaim(ctx, h.options.Namespace, h.buildPVC(status, req)); err != nil {
		jsonError(w, http.StatusInternalServerError, "ensure persistent volume claim failed: "+err.Error())
		return
	}
	jsonResponse(w, http.StatusOK, status)
}

func (h *Handler) handleGet(w http.ResponseWriter, r *http.Request, workspaceID, projectID, bindingID string) {
	ctx := r.Context()
	secretName, pvName, pvcName := names(workspaceID, projectID, bindingID)
	secret, err := h.k8sClient.GetSecret(ctx, h.options.Namespace, secretName)
	if err != nil {
		if errors.IsNotFound(err) {
			http.Error(w, "not found", http.StatusNotFound)
			return
		}
		jsonError(w, http.StatusInternalServerError, err.Error())
		return
	}
	pv, err := h.k8sClient.GetPersistentVolume(ctx, pvName)
	if err != nil {
		if errors.IsNotFound(err) {
			http.Error(w, "not found", http.StatusNotFound)
			return
		}
		jsonError(w, http.StatusInternalServerError, err.Error())
		return
	}
	pvc, err := h.k8sClient.GetPersistentVolumeClaim(ctx, h.options.Namespace, pvcName)
	if err != nil {
		if errors.IsNotFound(err) {
			http.Error(w, "not found", http.StatusNotFound)
			return
		}
		jsonError(w, http.StatusInternalServerError, err.Error())
		return
	}
	status := BindingStatus{
		BindingID:        bindingID,
		WorkspaceID:      workspaceID,
		ProjectID:        projectID,
		FileLibraryID:    valueOr(annotation(secret, "mbos.io/file-library-id"), label(secret, "mbos.io/file-library-id")),
		Status:           "ready",
		Namespace:        h.options.Namespace,
		SecretName:       secretName,
		PVName:           pvName,
		PVCName:          pvcName,
		VolumeHandle:     valueOr(annotation(pv, "mbos.io/volume-handle"), volumeHandle(workspaceID, projectID, bindingID)),
		FilesystemName:   secretString(secret, "name"),
		MountPath:        "/workspace",
		StorageClassName: derefString(pvc.Spec.StorageClassName),
		MountOptions:     pv.Spec.MountOptions,
		Subdir:           pv.Spec.CSI.VolumeAttributes["subdir"],
		CreatedAt:        annotation(secret, "mbos.io/created-at"),
		UpdatedAt:        annotation(secret, "mbos.io/updated-at"),
	}
	jsonResponse(w, http.StatusOK, status)
}

func (h *Handler) handleDelete(w http.ResponseWriter, r *http.Request, workspaceID, projectID, bindingID string) {
	ctx := r.Context()
	secretName, pvName, pvcName := names(workspaceID, projectID, bindingID)
	_ = h.k8sClient.DeletePersistentVolumeClaim(ctx, h.options.Namespace, pvcName)
	_ = h.k8sClient.DeletePersistentVolume(ctx, pvName)
	_ = h.k8sClient.DeleteSecret(ctx, h.options.Namespace, secretName)
	jsonResponse(w, http.StatusOK, map[string]string{"message": "binding deleted"})
}

func (h *Handler) buildSecret(status BindingStatus, req EnsureRequest) *v1.Secret {
	storageEndpoint := firstNonEmpty(req.StorageEndpoint, h.options.StorageEndpoint)
	return &v1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      status.SecretName,
			Namespace: h.options.Namespace,
			Labels: map[string]string{
				"app.kubernetes.io/managed-by": "mbos-sandbox-v1",
				"mbos.io/file-library-id":      status.FileLibraryID,
			},
			Annotations: map[string]string{
				"mbos.io/workspace-id": workspaceID(status),
				"mbos.io/project-id":   projectID(status),
				"mbos.io/created-at":   status.CreatedAt,
				"mbos.io/updated-at":   status.UpdatedAt,
			},
		},
		Type: "Opaque",
		StringData: map[string]string{
			"name":       req.FilesystemName,
			"metaurl":    req.MetadataURL,
			"storage":    "s3",
			"bucket":     strings.TrimRight(storageEndpoint, "/") + "/" + deterministicBucket(status.FileLibraryID),
			"access-key": strings.TrimSpace(h.options.StorageAccessKey),
			"secret-key": strings.TrimSpace(h.options.StorageSecretKey),
		},
	}
}

func (h *Handler) buildPV(status BindingStatus, req EnsureRequest) *v1.PersistentVolume {
	capacity := firstNonEmpty(req.StorageCapacity, h.options.StorageCapacity, "1Pi")
	attrs := map[string]string{}
	if status.Subdir != "" {
		attrs["subdir"] = status.Subdir
	}
	if value := firstNonEmpty(req.MountServiceAccount, h.options.MountServiceAccount); value != "" {
		attrs["juicefs/mount-service-account"] = value
	}
	if value := firstNonEmpty(req.MountImage, h.options.MountImage); value != "" {
		attrs["juicefs/mount-image"] = value
	}
	return &v1.PersistentVolume{
		ObjectMeta: metav1.ObjectMeta{
			Name: status.PVName,
			Labels: map[string]string{
				"app.kubernetes.io/managed-by": "mbos-sandbox-v1",
				"mbos.io/file-library-id":      status.FileLibraryID,
			},
			Annotations: map[string]string{
				"mbos.io/volume-handle": status.VolumeHandle,
			},
		},
		Spec: v1.PersistentVolumeSpec{
			Capacity:                      v1.ResourceList{v1.ResourceStorage: resource.MustParse(capacity)},
			AccessModes:                   []v1.PersistentVolumeAccessMode{v1.ReadWriteMany},
			PersistentVolumeReclaimPolicy: v1.PersistentVolumeReclaimRetain,
			VolumeMode:                    func() *v1.PersistentVolumeMode { m := v1.PersistentVolumeFilesystem; return &m }(),
			StorageClassName:              status.StorageClassName,
			MountOptions:                  status.MountOptions,
			PersistentVolumeSource: v1.PersistentVolumeSource{
				CSI: &v1.CSIPersistentVolumeSource{
					Driver:           firstNonEmpty(h.options.CSIDriver, "csi.juicefs.com"),
					VolumeHandle:     status.VolumeHandle,
					FSType:           "juicefs",
					VolumeAttributes: attrs,
					NodePublishSecretRef: &v1.SecretReference{
						Name:      status.SecretName,
						Namespace: h.options.Namespace,
					},
				},
			},
		},
	}
}

func (h *Handler) buildPVC(status BindingStatus, req EnsureRequest) *v1.PersistentVolumeClaim {
	capacity := firstNonEmpty(req.StorageCapacity, h.options.StorageCapacity, "1Pi")
	return &v1.PersistentVolumeClaim{
		ObjectMeta: metav1.ObjectMeta{
			Name:      status.PVCName,
			Namespace: h.options.Namespace,
			Labels: map[string]string{
				"app.kubernetes.io/managed-by": "mbos-sandbox-v1",
				"mbos.io/file-library-id":      status.FileLibraryID,
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

var validNameRegex = regexp.MustCompile(`^[a-z0-9_-]{1,63}$`)

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

func names(workspaceID, projectID, bindingID string) (secretName, pvName, pvcName string) {
	suffix := sanitizeK8sName(fmt.Sprintf("%s-%s-%s", workspaceID, projectID, bindingID), "binding")
	return "juicefs-secret-" + suffix, "juicefs-pv-" + suffix, "juicefs-pvc-" + suffix
}

func PVCName(workspaceID, projectID, bindingID string) string {
	_, _, pvcName := names(workspaceID, projectID, bindingID)
	return pvcName
}

func volumeHandle(workspaceID, projectID, bindingID string) string {
	return sanitizeK8sName(fmt.Sprintf("juicefs-%s-%s-%s", workspaceID, projectID, bindingID), "juicefs-volume")
}

func deterministicBucket(fileLibraryID string) string {
	return sanitizeK8sName("jfs-lib-"+strings.ReplaceAll(fileLibraryID, "_", "-"), "jfs-lib")
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return strings.TrimSpace(value)
		}
	}
	return ""
}

func firstSlice(a []string, b []string) []string {
	if len(a) > 0 {
		return compact(a)
	}
	return compact(b)
}

func compact(values []string) []string {
	result := make([]string, 0, len(values))
	for _, value := range values {
		if trimmed := strings.TrimSpace(value); trimmed != "" {
			result = append(result, trimmed)
		}
	}
	return result
}

func annotation[T interface{ GetAnnotations() map[string]string }](obj T, key string) string {
	return obj.GetAnnotations()[key]
}

func label[T interface{ GetLabels() map[string]string }](obj T, key string) string {
	return obj.GetLabels()[key]
}

func secretString(secret *v1.Secret, key string) string {
	return string(secret.Data[key])
}

func workspaceID(status BindingStatus) string { return status.WorkspaceID }
func projectID(status BindingStatus) string   { return status.ProjectID }

func valueOr(value string, fallback string) string {
	if strings.TrimSpace(value) == "" {
		return fallback
	}
	return value
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

func jsonError(w http.ResponseWriter, status int, message string) {
	jsonResponse(w, status, map[string]string{"error": message})
}
