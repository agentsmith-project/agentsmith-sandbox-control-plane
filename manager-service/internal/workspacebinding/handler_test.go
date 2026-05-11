package workspacebinding

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/sandbox/manager/internal/afscp"
	v1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime/schema"
)

type fakeK8sClient struct {
	pv           *v1.PersistentVolume
	pvc          *v1.PersistentVolumeClaim
	pods         []v1.Pod
	listPodsErr  error
	deletePVErr  error
	deletePVCErr error
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
	if f.deletePVErr != nil {
		return f.deletePVErr
	}
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
	if f.deletePVCErr != nil {
		return f.deletePVCErr
	}
	f.pvc = nil
	return nil
}
func (f *fakeK8sClient) ListPods(_ context.Context, _ string, _ metav1.ListOptions) (*v1.PodList, error) {
	if f.listPodsErr != nil {
		return nil, f.listPodsErr
	}
	return &v1.PodList{Items: append([]v1.Pod(nil), f.pods...)}, nil
}

type fakeAFSCPClient struct {
	plan           afscp.OrchestratorMountPlan
	err            error
	namespaceID    string
	mountBindingID string
	correlationID  string
	calls          int
}

func (f *fakeAFSCPClient) GetOrchestratorMountPlan(_ context.Context, namespaceID, mountBindingID, correlationID string) (afscp.OrchestratorMountPlan, error) {
	f.calls++
	f.namespaceID = namespaceID
	f.mountBindingID = mountBindingID
	f.correlationID = correlationID
	if f.err != nil {
		return afscp.OrchestratorMountPlan{}, f.err
	}
	return f.plan, nil
}

func validPlan() afscp.OrchestratorMountPlan {
	return afscp.OrchestratorMountPlan{
		MountBindingID:      "wmb_demo",
		VolumeID:            "vol_demo",
		PayloadVolumeSubdir: "afscp/ns_demo/repos/repo_demo/payload",
		MountPath:           "/home/task-demo",
		ReadOnly:            true,
		SecretRef:           afscp.SecretRef{Namespace: "afscp-mounts", Name: "juicefs-vol-demo"},
		SecurityPolicy:      afscp.SecurityPolicy{RunAsNonRoot: true, AllowPrivileged: false, JVSControlOutsidePayload: true},
	}
}

func TestEnsureAndGetBindingUsesAFSCPPlan(t *testing.T) {
	client := &fakeK8sClient{}
	afscpClient := &fakeAFSCPClient{plan: validPlan()}
	handler := NewHandler(client, Options{
		Namespace:        "sandbox-workloads",
		CSIDriver:        "csi.juicefs.com",
		StorageCapacity:  "1Pi",
		StorageClassName: "juicefs-static",
		AFSCPClient:      afscpClient,
	})

	payload := `{"namespace_id":"ns_demo","mount_binding_id":"wmb_demo"}`
	req := httptest.NewRequest(http.MethodPut, "/v1/workspaces/ws_demo/projects/proj_demo/workspace-bindings/wmb_demo", strings.NewReader(payload))
	req.Header.Set("X-Correlation-Id", "corr-test")
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d body=%s", rec.Code, rec.Body.String())
	}
	if afscpClient.calls != 1 || afscpClient.namespaceID != "ns_demo" || afscpClient.mountBindingID != "wmb_demo" || afscpClient.correlationID != "corr-test" {
		t.Fatalf("unexpected afscp call: %#v", afscpClient)
	}
	if client.pv == nil || client.pvc == nil {
		t.Fatalf("expected pv/pvc to be ensured")
	}
	for resourceName, labels := range map[string]map[string]string{
		"pv":  client.pv.Labels,
		"pvc": client.pvc.Labels,
	} {
		if got := labels["app.kubernetes.io/managed-by"]; got != "agentsmith" {
			t.Fatalf("expected %s owner label to be agentsmith, got %q", resourceName, got)
		}
	}
	if client.pv.Spec.CSI == nil {
		t.Fatalf("expected CSI PV")
	}
	if !hasMountOption(client.pv.Spec.MountOptions, "subdir=afscp/ns_demo/repos/repo_demo/payload") {
		t.Fatalf("expected PV mountOptions to carry AFSCP payload subdir, got %#v", client.pv.Spec.MountOptions)
	}
	if got := client.pv.Spec.CSI.VolumeAttributes["subdir"]; got != "" {
		t.Fatalf("VolumeAttributes[subdir] must not be the isolation source, got %q", got)
	}
	if got := client.pv.Spec.CSI.NodePublishSecretRef; got == nil || got.Namespace != "afscp-mounts" || got.Name != "juicefs-vol-demo" {
		t.Fatalf("expected secret_ref from AFSCP plan, got %#v", got)
	}
	if _, ok := client.pvc.Annotations[annotationMountPath]; !ok {
		t.Fatalf("expected pvc plan annotations, got %#v", client.pvc.Annotations)
	}
	renderedPV, _ := json.Marshal(client.pv)
	renderedPVC, _ := json.Marshal(client.pvc)
	legacyOwner := "mbos-sandbox" + "-v1"
	if strings.Contains(string(renderedPV), legacyOwner) || strings.Contains(string(renderedPVC), legacyOwner) {
		t.Fatalf("PV/PVC must not retain legacy owner label %q", legacyOwner)
	}
	for _, forbidden := range []string{"metadata_url", "metaurl", "bucket", "access-key", "secret-key", "postgres://", "minio"} {
		if strings.Contains(string(renderedPV), forbidden) || strings.Contains(string(renderedPVC), forbidden) {
			t.Fatalf("raw storage credential marker %q leaked into PV/PVC", forbidden)
		}
	}
	getReq := httptest.NewRequest(http.MethodGet, "/v1/workspaces/ws_demo/projects/proj_demo/workspace-bindings/wmb_demo", nil)
	getRec := httptest.NewRecorder()
	handler.ServeHTTP(getRec, getReq)
	if getRec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d body=%s", getRec.Code, getRec.Body.String())
	}
	var status BindingStatus
	if err := json.Unmarshal(getRec.Body.Bytes(), &status); err != nil {
		t.Fatalf("unmarshal status: %v", err)
	}
	if status.PVCName == "" || status.MountBindingID != "wmb_demo" || status.NamespaceID != "ns_demo" || status.MountPath != "/home/task-demo" || !status.ReadOnly {
		t.Fatalf("unexpected binding status: %+v", status)
	}
	if strings.Contains(getRec.Body.String(), "payload_volume_subdir") || strings.Contains(getRec.Body.String(), "secret_ref") || strings.Contains(getRec.Body.String(), "juicefs-vol-demo") {
		t.Fatalf("binding status leaked orchestrator-only fields: %s", getRec.Body.String())
	}
}

func TestEnsureBindingMountOptionsAreRepoPayloadScoped(t *testing.T) {
	firstPlan := validPlan()
	firstPlan.MountBindingID = "wmb_repo_a"
	firstPlan.PayloadVolumeSubdir = "afscp/ns_demo/repos/repo_a/payload"
	firstPlan.MountPath = "/home/repo-a"
	secondPlan := validPlan()
	secondPlan.MountBindingID = "wmb_repo_b"
	secondPlan.PayloadVolumeSubdir = "afscp/ns_demo/repos/repo_b/payload"
	secondPlan.MountPath = "/home/repo-b"

	firstPV := ensureBindingPVForTest(t, firstPlan)
	secondPV := ensureBindingPVForTest(t, secondPlan)

	if !hasMountOption(firstPV.Spec.MountOptions, "subdir=afscp/ns_demo/repos/repo_a/payload") {
		t.Fatalf("first PV missing repo-scoped subdir mount option: %#v", firstPV.Spec.MountOptions)
	}
	if !hasMountOption(secondPV.Spec.MountOptions, "subdir=afscp/ns_demo/repos/repo_b/payload") {
		t.Fatalf("second PV missing repo-scoped subdir mount option: %#v", secondPV.Spec.MountOptions)
	}
	if strings.Join(firstPV.Spec.MountOptions, "\n") == strings.Join(secondPV.Spec.MountOptions, "\n") {
		t.Fatalf("distinct repo payloads must not share identical PV mount options: %#v", firstPV.Spec.MountOptions)
	}
	for name, pv := range map[string]*v1.PersistentVolume{"first": firstPV, "second": secondPV} {
		if pv.Spec.CSI == nil {
			t.Fatalf("%s PV must be CSI-backed", name)
		}
		if got := pv.Spec.CSI.VolumeAttributes["subdir"]; got != "" {
			t.Fatalf("%s PV must not rely on VolumeAttributes[subdir], got %q", name, got)
		}
	}
}

func ensureBindingPVForTest(t *testing.T, plan afscp.OrchestratorMountPlan) *v1.PersistentVolume {
	t.Helper()
	client := &fakeK8sClient{}
	handler := NewHandler(client, Options{
		Namespace:        "sandbox-workloads",
		CSIDriver:        "csi.juicefs.com",
		StorageCapacity:  "1Pi",
		StorageClassName: "juicefs-static",
		AFSCPClient:      &fakeAFSCPClient{plan: plan},
	})
	payload := `{"namespace_id":"ns_demo","mount_binding_id":"` + plan.MountBindingID + `"}`
	req := httptest.NewRequest(http.MethodPut, "/v1/workspaces/ws_demo/projects/proj_demo/workspace-bindings/"+plan.MountBindingID, strings.NewReader(payload))
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d body=%s", rec.Code, rec.Body.String())
	}
	if client.pv == nil {
		t.Fatalf("expected PV to be ensured")
	}
	return client.pv
}

func hasMountOption(options []string, want string) bool {
	for _, option := range options {
		if option == want {
			return true
		}
	}
	return false
}

func TestEnsureBindingRejectsRawJuiceFSFields(t *testing.T) {
	handler := NewHandler(&fakeK8sClient{}, Options{
		Namespace:   "sandbox-workloads",
		AFSCPClient: &fakeAFSCPClient{plan: validPlan()},
	})
	payload := `{"namespace_id":"ns_demo","mount_binding_id":"wmb_demo","metadata_url":"postgres://juicefs:secret@pg/jfs","storage_endpoint":"http://minio:9000","bucket":"raw"}`
	req := httptest.NewRequest(http.MethodPut, "/v1/workspaces/ws_demo/projects/proj_demo/workspace-bindings/wmb_demo", strings.NewReader(payload))
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d body=%s", rec.Code, rec.Body.String())
	}
}

func TestEnsureBindingFailsClosedWhenAFSCPPlanUnavailable(t *testing.T) {
	client := &fakeK8sClient{}
	handler := NewHandler(client, Options{
		Namespace:   "sandbox-workloads",
		AFSCPClient: &fakeAFSCPClient{plan: validPlan(), err: errors.New("afscp down")},
	})
	payload := `{"namespace_id":"ns_demo","mount_binding_id":"wmb_demo"}`
	req := httptest.NewRequest(http.MethodPut, "/v1/workspaces/ws_demo/projects/proj_demo/workspace-bindings/wmb_demo", strings.NewReader(payload))
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusBadGateway {
		t.Fatalf("expected 502, got %d body=%s", rec.Code, rec.Body.String())
	}
	if client.pv != nil || client.pvc != nil {
		t.Fatalf("expected no k8s resources when AFSCP plan is unavailable")
	}
}

func TestDeleteBinding(t *testing.T) {
	client := &fakeK8sClient{
		pv:  &v1.PersistentVolume{ObjectMeta: metav1.ObjectMeta{Name: "pv"}},
		pvc: &v1.PersistentVolumeClaim{ObjectMeta: metav1.ObjectMeta{Name: "pvc"}},
	}
	handler := NewHandler(client, Options{Namespace: "sandbox-workloads"})
	req := httptest.NewRequest(http.MethodDelete, "/v1/workspaces/ws_demo/projects/proj_demo/workspace-bindings/wmb_demo", nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d body=%s", rec.Code, rec.Body.String())
	}
	if client.pv != nil || client.pvc != nil {
		t.Fatalf("expected resources to be deleted")
	}
}

func TestDeleteBindingRejectsActiveWorkload(t *testing.T) {
	client := &fakeK8sClient{
		pv:  &v1.PersistentVolume{ObjectMeta: metav1.ObjectMeta{Name: "pv"}},
		pvc: &v1.PersistentVolumeClaim{ObjectMeta: metav1.ObjectMeta{Name: "pvc"}},
		pods: []v1.Pod{
			{
				ObjectMeta: metav1.ObjectMeta{
					Name: "workload-active",
					Labels: map[string]string{
						"app":          "managed-workload",
						"workspace_id": "ws_demo",
						"project_id":   "proj_demo",
					},
					Annotations: map[string]string{
						annotationAFSCPMountBindingID: "wmb_demo",
					},
				},
			},
		},
	}
	handler := NewHandler(client, Options{Namespace: "sandbox-workloads"})
	req := httptest.NewRequest(http.MethodDelete, "/v1/workspaces/ws_demo/projects/proj_demo/workspace-bindings/wmb_demo", nil)
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusConflict {
		t.Fatalf("expected 409, got %d body=%s", rec.Code, rec.Body.String())
	}
	if client.pv == nil || client.pvc == nil {
		t.Fatalf("active workload must block PV/PVC deletion")
	}
}

func TestDeleteBindingReturnsErrorWhenPVCDeleteFails(t *testing.T) {
	client := &fakeK8sClient{
		pv:           &v1.PersistentVolume{ObjectMeta: metav1.ObjectMeta{Name: "pv"}},
		pvc:          &v1.PersistentVolumeClaim{ObjectMeta: metav1.ObjectMeta{Name: "pvc"}},
		deletePVCErr: errors.New("pvc delete failed"),
	}
	handler := NewHandler(client, Options{Namespace: "sandbox-workloads"})
	req := httptest.NewRequest(http.MethodDelete, "/v1/workspaces/ws_demo/projects/proj_demo/workspace-bindings/wmb_demo", nil)
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("expected 500, got %d body=%s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "pvc delete failed") {
		t.Fatalf("expected pvc delete error in response, got %s", rec.Body.String())
	}
}

func TestDeleteBindingReturnsErrorWhenPVDeleteFails(t *testing.T) {
	client := &fakeK8sClient{
		pv:          &v1.PersistentVolume{ObjectMeta: metav1.ObjectMeta{Name: "pv"}},
		pvc:         &v1.PersistentVolumeClaim{ObjectMeta: metav1.ObjectMeta{Name: "pvc"}},
		deletePVErr: errors.New("pv delete failed"),
	}
	handler := NewHandler(client, Options{Namespace: "sandbox-workloads"})
	req := httptest.NewRequest(http.MethodDelete, "/v1/workspaces/ws_demo/projects/proj_demo/workspace-bindings/wmb_demo", nil)
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("expected 500, got %d body=%s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "pv delete failed") {
		t.Fatalf("expected pv delete error in response, got %s", rec.Body.String())
	}
}
