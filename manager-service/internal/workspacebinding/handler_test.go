package workspacebinding

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	v1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime/schema"
)

type fakeK8sClient struct {
	secret *v1.Secret
	pv     *v1.PersistentVolume
	pvc    *v1.PersistentVolumeClaim
}

func (f *fakeK8sClient) EnsureSecret(_ context.Context, _ string, secret *v1.Secret) error {
	if len(secret.StringData) > 0 {
		secret.Data = make(map[string][]byte, len(secret.StringData))
		for key, value := range secret.StringData {
			secret.Data[key] = []byte(value)
		}
		secret.StringData = nil
	}
	f.secret = secret
	return nil
}
func (f *fakeK8sClient) GetSecret(_ context.Context, _ string, _ string) (*v1.Secret, error) {
	if f.secret == nil {
		return nil, apierrors.NewNotFound(schema.GroupResource{Resource: "secrets"}, "missing")
	}
	return f.secret, nil
}
func (f *fakeK8sClient) DeleteSecret(_ context.Context, _ string, _ string) error {
	f.secret = nil
	return nil
}
func (f *fakeK8sClient) EnsurePersistentVolume(_ context.Context, volume *v1.PersistentVolume) error {
	f.pv = volume
	return nil
}
func (f *fakeK8sClient) GetPersistentVolume(_ context.Context, _ string) (*v1.PersistentVolume, error) {
	if f.pv == nil {
		return nil, apierrors.NewNotFound(schema.GroupResource{Resource: "persistentvolumes"}, "missing")
	}
	return f.pv, nil
}
func (f *fakeK8sClient) DeletePersistentVolume(_ context.Context, _ string) error {
	f.pv = nil
	return nil
}
func (f *fakeK8sClient) EnsurePersistentVolumeClaim(_ context.Context, _ string, claim *v1.PersistentVolumeClaim) error {
	f.pvc = claim
	return nil
}
func (f *fakeK8sClient) GetPersistentVolumeClaim(_ context.Context, _ string, _ string) (*v1.PersistentVolumeClaim, error) {
	if f.pvc == nil {
		return nil, apierrors.NewNotFound(schema.GroupResource{Resource: "persistentvolumeclaims"}, "missing")
	}
	return f.pvc, nil
}
func (f *fakeK8sClient) DeletePersistentVolumeClaim(_ context.Context, _ string, _ string) error {
	f.pvc = nil
	return nil
}

func TestEnsureAndGetBinding(t *testing.T) {
	client := &fakeK8sClient{}
	handler := NewHandler(client, Options{
		Namespace:             "sandbox-workloads",
		CSIDriver:             "csi.juicefs.com",
		StorageCapacity:       "1Pi",
		StorageClassName:      "juicefs-static",
		MountOptions:          []string{"writeback_cache"},
		StorageEndpoint:       "http://minio.internal:19000",
		StorageCredentialSeed: "seed-demo",
	})

	payload := `{"file_library_id":"flib_demo","filesystem_name":"jfs_demo","metadata_url":"postgres://juicefs:secret@pg:5432/jfs_demo","subdir":"/workspaces/ws/flib_demo"}`
	req := httptest.NewRequest(http.MethodPut, "/v1/workspaces/ws_demo/projects/proj_demo/workspace-bindings/flib_demo", strings.NewReader(payload))
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d body=%s", rec.Code, rec.Body.String())
	}
	if client.secret == nil || client.pv == nil || client.pvc == nil {
		t.Fatalf("expected secret/pv/pvc to be ensured")
	}
	if got := client.pv.Spec.CSI.VolumeAttributes["subdir"]; got != "/workspaces/ws/flib_demo" {
		t.Fatalf("expected subdir to be propagated, got %q", got)
	}
	getReq := httptest.NewRequest(http.MethodGet, "/v1/workspaces/ws_demo/projects/proj_demo/workspace-bindings/flib_demo", nil)
	getRec := httptest.NewRecorder()
	handler.ServeHTTP(getRec, getReq)
	if getRec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d body=%s", getRec.Code, getRec.Body.String())
	}
	var status BindingStatus
	if err := json.Unmarshal(getRec.Body.Bytes(), &status); err != nil {
		t.Fatalf("unmarshal status: %v", err)
	}
	if status.PVCName == "" || status.FileLibraryID != "flib_demo" || status.MountPath != "/workspace" {
		t.Fatalf("unexpected binding status: %+v", status)
	}
}

func TestDeleteBinding(t *testing.T) {
	client := &fakeK8sClient{
		secret: &v1.Secret{ObjectMeta: metav1.ObjectMeta{Name: "s"}},
		pv:     &v1.PersistentVolume{ObjectMeta: metav1.ObjectMeta{Name: "pv"}},
		pvc:    &v1.PersistentVolumeClaim{ObjectMeta: metav1.ObjectMeta{Name: "pvc"}},
	}
	handler := NewHandler(client, Options{Namespace: "sandbox-workloads"})
	req := httptest.NewRequest(http.MethodDelete, "/v1/workspaces/ws_demo/projects/proj_demo/workspace-bindings/flib_demo", nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d body=%s", rec.Code, rec.Body.String())
	}
	if client.secret != nil || client.pv != nil || client.pvc != nil {
		t.Fatalf("expected resources to be deleted")
	}
}
