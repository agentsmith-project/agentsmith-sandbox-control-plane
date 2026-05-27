package workspacebinding

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"log"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/agentsmith-project/agentsmith-sandbox-control-plane/internal/afscp"
	"github.com/agentsmith-project/agentsmith-sandbox-control-plane/internal/observability"
	"github.com/agentsmith-project/agentsmith-sandbox-control-plane/internal/workloadfacts"
	v1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/runtime/schema"
)

type fakeK8sClient struct {
	pv            *v1.PersistentVolume
	pvc           *v1.PersistentVolumeClaim
	pods          []v1.Pod
	listSelectors []string
	listPodsErr   error
	deletePVErr   error
	deletePVCErr  error
}

type testErrorEnvelope struct {
	Error struct {
		Code      string `json:"code"`
		Message   string `json:"message"`
		RequestID string `json:"request_id"`
	} `json:"error"`
}

func decodeErrorEnvelope(t *testing.T, rec *httptest.ResponseRecorder) testErrorEnvelope {
	t.Helper()
	var body testErrorEnvelope
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode error envelope: %v", err)
	}
	return body
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
func (f *fakeK8sClient) ListPods(_ context.Context, _ string, opts metav1.ListOptions) (*v1.PodList, error) {
	f.listSelectors = append(f.listSelectors, opts.LabelSelector)
	if f.listPodsErr != nil {
		return nil, f.listPodsErr
	}
	if strings.TrimSpace(opts.LabelSelector) == "" {
		return &v1.PodList{Items: append([]v1.Pod(nil), f.pods...)}, nil
	}
	selector, err := labels.Parse(opts.LabelSelector)
	if err != nil {
		return nil, err
	}
	items := make([]v1.Pod, 0, len(f.pods))
	for _, pod := range f.pods {
		if selector.Matches(labels.Set(pod.Labels)) {
			items = append(items, pod)
		}
	}
	return &v1.PodList{Items: items}, nil
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

func captureStandardLog(t *testing.T) *bytes.Buffer {
	t.Helper()
	buf := &bytes.Buffer{}
	old := log.Writer()
	log.SetOutput(buf)
	t.Cleanup(func() { log.SetOutput(old) })
	return buf
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
	assertMountOptionsInclude(t, client.pv.Spec.MountOptions,
		"subdir=afscp/ns_demo/repos/repo_demo/payload",
		"attr-cache=0s",
		"entry-cache=0s",
		"dir-entry-cache=0s",
		"negative-entry-cache=0s",
	)
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

func TestEnsureBindingUsesRequestIDContextForAFSCPCorrelation(t *testing.T) {
	client := &fakeK8sClient{}
	afscpClient := &fakeAFSCPClient{plan: validPlan()}
	handler := NewHandler(client, Options{
		Namespace:        "sandbox-workloads",
		CSIDriver:        "csi.juicefs.com",
		StorageCapacity:  "1Pi",
		StorageClassName: "juicefs-static",
		AFSCPClient:      afscpClient,
	})
	wrapped := observability.RequestIDMiddleware("X-ASBCP-Request-ID")(handler)

	payload := `{"namespace_id":"ns_demo","mount_binding_id":"wmb_demo"}`
	req := httptest.NewRequest(http.MethodPut, "/v1/workspaces/ws_demo/projects/proj_demo/workspace-bindings/wmb_demo", strings.NewReader(payload))
	req.Header.Set("X-ASBCP-Request-ID", "custom-request-id")
	req.Header.Set("X-Correlation-Id", "stale-correlation-id")
	rec := httptest.NewRecorder()

	wrapped.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d body=%s", rec.Code, rec.Body.String())
	}
	if afscpClient.correlationID != "custom-request-id" {
		t.Fatalf("AFSCP correlation id = %q, want custom request id from middleware context", afscpClient.correlationID)
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

func TestBuildPVIncludesPayloadSubdirAndJuiceFSConsistencyMountOptions(t *testing.T) {
	plan := validPlan()
	status := BindingStatus{
		PVName:              "juicefs-pv-test",
		WorkspaceID:         "ws_demo",
		ProjectID:           "proj_demo",
		MountBindingID:      plan.MountBindingID,
		VolumeHandle:        "juicefs-test",
		NamespaceID:         "ns_demo",
		VolumeID:            plan.VolumeID,
		PayloadVolumeSubdir: plan.PayloadVolumeSubdir,
		MountPath:           plan.MountPath,
		ReadOnly:            plan.ReadOnly,
		StorageClassName:    "juicefs-static",
		CreatedAt:           "2026-05-27T00:00:00Z",
		UpdatedAt:           "2026-05-27T00:00:00Z",
	}
	handler := NewHandler(&fakeK8sClient{}, Options{
		CSIDriver:       "csi.juicefs.com",
		StorageCapacity: "1Pi",
	})

	pv := handler.buildPV(status, plan)

	assertMountOptionsInclude(t, pv.Spec.MountOptions,
		"subdir=afscp/ns_demo/repos/repo_demo/payload",
		"attr-cache=0s",
		"entry-cache=0s",
		"dir-entry-cache=0s",
		"negative-entry-cache=0s",
	)
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

func assertMountOptionsInclude(t *testing.T, options []string, wants ...string) {
	t.Helper()
	for _, want := range wants {
		if !hasMountOption(options, want) {
			t.Fatalf("PV mountOptions missing %q, got %#v", want, options)
		}
	}
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
	afscpErr := errors.New("afscp dependency failed token=raw-secret password=p@ss for mount wmb_demo")
	handler := NewHandler(client, Options{
		Namespace:   "sandbox-workloads",
		AFSCPClient: &fakeAFSCPClient{plan: validPlan(), err: afscpErr},
	})
	payload := `{"namespace_id":"ns_demo","mount_binding_id":"wmb_demo"}`
	req := httptest.NewRequest(http.MethodPut, "/v1/workspaces/ws_demo/projects/proj_demo/workspace-bindings/wmb_demo", strings.NewReader(payload))
	req.Header.Set("X-Request-Id", "req-afscp")
	rec := httptest.NewRecorder()
	logs := captureStandardLog(t)

	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusBadGateway {
		t.Fatalf("expected 502, got %d body=%s", rec.Code, rec.Body.String())
	}
	var body struct {
		Error struct {
			Code      string `json:"code"`
			Message   string `json:"message"`
			RequestID string `json:"request_id"`
		} `json:"error"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode error envelope: %v", err)
	}
	if body.Error.Code != "dependency_failure" || body.Error.Message == "" || body.Error.RequestID != "req-afscp" {
		t.Fatalf("unexpected error envelope: %+v", body.Error)
	}
	for _, raw := range []string{"raw-secret", "wmb_demo"} {
		if strings.Contains(rec.Body.String(), raw) {
			t.Fatalf("AFSCP raw error detail leaked to API client via %q: %s", raw, rec.Body.String())
		}
	}
	if client.pv != nil || client.pvc != nil {
		t.Fatalf("expected no k8s resources when AFSCP plan is unavailable")
	}
	logOutput := logs.String()
	for _, token := range []string{"AFSCP orchestrator mount plan is unavailable", "workspace=ws_demo", "project=proj_demo", "mount_binding_id=wmb_demo", "request_id=req-afscp", "[REDACTED]"} {
		if !strings.Contains(logOutput, token) {
			t.Fatalf("expected redacted AFSCP failure log token %q in %q", token, logOutput)
		}
	}
	for _, leaked := range []string{"raw-secret", "p@ss"} {
		if strings.Contains(logOutput, leaked) {
			t.Fatalf("AFSCP failure log leaked %q in %q", leaked, logOutput)
		}
	}
}

func TestDeleteBinding(t *testing.T) {
	client := &fakeK8sClient{
		pv:  &v1.PersistentVolume{ObjectMeta: metav1.ObjectMeta{Name: "pv"}},
		pvc: &v1.PersistentVolumeClaim{ObjectMeta: metav1.ObjectMeta{Name: "pvc"}},
	}
	handler := NewHandler(client, Options{Namespace: "sandbox-workloads", WorkloadFacts: workloadfacts.NewMemoryStore()})
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
	facts := workloadfacts.NewMemoryStore()
	if err := facts.Save(context.Background(), workloadfacts.Fact{
		WorkspaceID:        "ws_demo",
		ProjectID:          "proj_demo",
		WorkloadID:         "active",
		WorkspaceBindingID: "wmb_demo",
		NamespaceID:        "ns_demo",
		MountBindingID:     "wmb_demo",
		PodName:            "workload-active",
	}); err != nil {
		t.Fatalf("save fact: %v", err)
	}
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
	handler := NewHandler(client, Options{Namespace: "sandbox-workloads", WorkloadFacts: facts})
	req := httptest.NewRequest(http.MethodDelete, "/v1/workspaces/ws_demo/projects/proj_demo/workspace-bindings/wmb_demo", nil)
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusConflict {
		t.Fatalf("expected 409, got %d body=%s", rec.Code, rec.Body.String())
	}
	body := decodeErrorEnvelope(t, rec)
	if body.Error.Code != "workspace_binding_release_incomplete" {
		t.Fatalf("expected stable release-incomplete code, got %+v", body.Error)
	}
	if client.pv == nil || client.pvc == nil {
		t.Fatalf("active workload must block PV/PVC deletion")
	}
}

func TestDeleteBindingBlocksLivePodWithoutFact(t *testing.T) {
	client := &fakeK8sClient{
		pv:  &v1.PersistentVolume{ObjectMeta: metav1.ObjectMeta{Name: "pv"}},
		pvc: &v1.PersistentVolumeClaim{ObjectMeta: metav1.ObjectMeta{Name: "pvc"}},
		pods: []v1.Pod{
			{
				ObjectMeta: metav1.ObjectMeta{
					Name: "workload-live",
					Labels: map[string]string{
						"app": "managed-workload",
					},
					Annotations: map[string]string{
						annotationWorkspaceID:         "ws_demo",
						annotationProjectID:           "proj_demo",
						annotationAFSCPMountBindingID: "wmb_demo",
						"mbos.io/workload-id":         "live",
					},
				},
			},
		},
	}
	handler := NewHandler(client, Options{Namespace: "sandbox-workloads", WorkloadFacts: workloadfacts.NewMemoryStore()})
	req := httptest.NewRequest(http.MethodDelete, "/v1/workspaces/ws_demo/projects/proj_demo/workspace-bindings/wmb_demo", nil)
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusConflict {
		t.Fatalf("expected 409, got %d body=%s", rec.Code, rec.Body.String())
	}
	body := decodeErrorEnvelope(t, rec)
	if body.Error.Code != "workspace_binding_release_incomplete" {
		t.Fatalf("expected stable release-incomplete code, got %+v", body.Error)
	}
	if client.pv == nil || client.pvc == nil {
		t.Fatalf("live pod without fact must block PV/PVC deletion")
	}
}

func TestDeleteBindingBlocksLivePodByPVCClaimName(t *testing.T) {
	expectedPVC := PVCName("ws_demo", "proj_demo", "wmb_demo")
	tests := []struct {
		name        string
		annotations map[string]string
	}{
		{
			name:        "missing annotations",
			annotations: nil,
		},
		{
			name: "drifted binding annotation",
			annotations: map[string]string{
				annotationWorkspaceID:         "ws_demo",
				annotationProjectID:           "proj_demo",
				annotationAFSCPMountBindingID: "wmb_other",
				"mbos.io/workload-id":         "drifted",
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			client := &fakeK8sClient{
				pv:  &v1.PersistentVolume{ObjectMeta: metav1.ObjectMeta{Name: "pv"}},
				pvc: &v1.PersistentVolumeClaim{ObjectMeta: metav1.ObjectMeta{Name: "pvc"}},
				pods: []v1.Pod{
					{
						ObjectMeta: metav1.ObjectMeta{
							Name:        "workload-live-pvc",
							Labels:      map[string]string{"app": "managed-workload"},
							Annotations: tt.annotations,
						},
						Spec: v1.PodSpec{
							Volumes: []v1.Volume{
								{
									Name: "workspace",
									VolumeSource: v1.VolumeSource{
										PersistentVolumeClaim: &v1.PersistentVolumeClaimVolumeSource{
											ClaimName: expectedPVC,
										},
									},
								},
							},
						},
					},
				},
			}
			handler := NewHandler(client, Options{Namespace: "sandbox-workloads", WorkloadFacts: workloadfacts.NewMemoryStore()})
			req := httptest.NewRequest(http.MethodDelete, "/v1/workspaces/ws_demo/projects/proj_demo/workspace-bindings/wmb_demo", nil)
			rec := httptest.NewRecorder()

			handler.ServeHTTP(rec, req)

			if rec.Code != http.StatusConflict {
				t.Fatalf("expected 409, got %d body=%s", rec.Code, rec.Body.String())
			}
			body := decodeErrorEnvelope(t, rec)
			if body.Error.Code != "workspace_binding_release_incomplete" {
				t.Fatalf("expected stable release-incomplete code, got %+v", body.Error)
			}
			if client.pv == nil || client.pvc == nil {
				t.Fatalf("pod volume claimName match must block PV/PVC deletion")
			}
		})
	}
}

func TestDeleteBindingScansAllPodsForPVCReferencesDespiteLabelDrift(t *testing.T) {
	expectedPVC := PVCName("ws_demo", "proj_demo", "wmb_demo")
	client := &fakeK8sClient{
		pv:  &v1.PersistentVolume{ObjectMeta: metav1.ObjectMeta{Name: "pv"}},
		pvc: &v1.PersistentVolumeClaim{ObjectMeta: metav1.ObjectMeta{Name: "pvc"}},
		pods: []v1.Pod{
			{
				ObjectMeta: metav1.ObjectMeta{
					Name:        "workload-label-drifted",
					Labels:      map[string]string{"app": "drifted-away"},
					Annotations: map[string]string{"mbos.io/workload-id": "label-drifted"},
				},
				Spec: v1.PodSpec{
					Volumes: []v1.Volume{
						{
							Name: "workspace",
							VolumeSource: v1.VolumeSource{
								PersistentVolumeClaim: &v1.PersistentVolumeClaimVolumeSource{
									ClaimName: expectedPVC,
								},
							},
						},
					},
				},
			},
		},
	}
	handler := NewHandler(client, Options{Namespace: "sandbox-workloads", WorkloadFacts: workloadfacts.NewMemoryStore()})
	req := httptest.NewRequest(http.MethodDelete, "/v1/workspaces/ws_demo/projects/proj_demo/workspace-bindings/wmb_demo", nil)
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusConflict {
		t.Fatalf("expected 409, got %d body=%s", rec.Code, rec.Body.String())
	}
	if len(client.listSelectors) != 1 || client.listSelectors[0] != "" {
		t.Fatalf("delete guard must scan all pods without a driftable label selector, got %#v", client.listSelectors)
	}
	body := decodeErrorEnvelope(t, rec)
	if body.Error.Code != "workspace_binding_release_incomplete" {
		t.Fatalf("expected stable release-incomplete code, got %+v", body.Error)
	}
	if client.pv == nil || client.pvc == nil {
		t.Fatalf("PVC reference from label-drifted pod must block PV/PVC deletion")
	}
}

func TestDeleteBindingBlocksWhenPodScanFails(t *testing.T) {
	client := &fakeK8sClient{
		pv:          &v1.PersistentVolume{ObjectMeta: metav1.ObjectMeta{Name: "pv"}},
		pvc:         &v1.PersistentVolumeClaim{ObjectMeta: metav1.ObjectMeta{Name: "pvc"}},
		listPodsErr: errors.New("pod scan unavailable"),
	}
	handler := NewHandler(client, Options{Namespace: "sandbox-workloads", WorkloadFacts: workloadfacts.NewMemoryStore()})
	req := httptest.NewRequest(http.MethodDelete, "/v1/workspaces/ws_demo/projects/proj_demo/workspace-bindings/wmb_demo", nil)
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusConflict {
		t.Fatalf("expected 409, got %d body=%s", rec.Code, rec.Body.String())
	}
	body := decodeErrorEnvelope(t, rec)
	if body.Error.Code != "workspace_binding_release_incomplete" {
		t.Fatalf("expected stable release-incomplete code, got %+v", body.Error)
	}
	if client.pv == nil || client.pvc == nil {
		t.Fatalf("pod scan failure must fail closed and keep PV/PVC")
	}
}

func TestDeleteBindingBlocksWhenFactStoreUnavailable(t *testing.T) {
	client := &fakeK8sClient{
		pv:  &v1.PersistentVolume{ObjectMeta: metav1.ObjectMeta{Name: "pv"}},
		pvc: &v1.PersistentVolumeClaim{ObjectMeta: metav1.ObjectMeta{Name: "pvc"}},
	}
	handler := NewHandler(client, Options{Namespace: "sandbox-workloads"})
	req := httptest.NewRequest(http.MethodDelete, "/v1/workspaces/ws_demo/projects/proj_demo/workspace-bindings/wmb_demo", nil)
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusConflict {
		t.Fatalf("expected fact-source-unavailable delete to return 409, got %d body=%s", rec.Code, rec.Body.String())
	}
	body := decodeErrorEnvelope(t, rec)
	if body.Error.Code != "workspace_binding_release_incomplete" {
		t.Fatalf("expected stable release-incomplete code, got %+v", body.Error)
	}
	if client.pv == nil || client.pvc == nil {
		t.Fatalf("fact source unavailable must not delete PV/PVC")
	}
}

func TestDeleteBindingBlocksUnreleasedWorkloadEvenWhenNoPodExists(t *testing.T) {
	facts := workloadfacts.NewMemoryStore()
	if err := facts.Save(context.Background(), workloadfacts.Fact{
		WorkspaceID:        "ws_demo",
		ProjectID:          "proj_demo",
		WorkloadID:         "wl_deleted_elsewhere",
		NamespaceID:        "ns_demo",
		MountBindingID:     "wmb_demo",
		WorkspaceBindingID: "wmb_demo",
		PodName:            "workload-wl-deleted-elsewhere",
		PodUID:             "uid-deleted-elsewhere",
		ReleaseDone:        false,
		PodDeleted:         true,
		TerminalStatusDone: false,
	}); err != nil {
		t.Fatalf("save fact: %v", err)
	}
	client := &fakeK8sClient{
		pv:  &v1.PersistentVolume{ObjectMeta: metav1.ObjectMeta{Name: "pv"}},
		pvc: &v1.PersistentVolumeClaim{ObjectMeta: metav1.ObjectMeta{Name: "pvc"}},
	}
	handler := NewHandler(client, Options{Namespace: "sandbox-workloads", WorkloadFacts: facts})
	req := httptest.NewRequest(http.MethodDelete, "/v1/workspaces/ws_demo/projects/proj_demo/workspace-bindings/wmb_demo", nil)
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusConflict {
		t.Fatalf("expected 409, got %d body=%s", rec.Code, rec.Body.String())
	}
	body := decodeErrorEnvelope(t, rec)
	if body.Error.Code != "workspace_binding_release_incomplete" {
		t.Fatalf("expected stable release-incomplete code, got %+v", body.Error)
	}
	if client.pv == nil || client.pvc == nil {
		t.Fatalf("non-terminal workload fact must block PV/PVC deletion even when no pod exists")
	}
}

func TestDeleteBindingReturnsErrorWhenPVCDeleteFails(t *testing.T) {
	client := &fakeK8sClient{
		pv:           &v1.PersistentVolume{ObjectMeta: metav1.ObjectMeta{Name: "pv"}},
		pvc:          &v1.PersistentVolumeClaim{ObjectMeta: metav1.ObjectMeta{Name: "pvc"}},
		deletePVCErr: errors.New("pvc delete failed"),
	}
	handler := NewHandler(client, Options{Namespace: "sandbox-workloads", WorkloadFacts: workloadfacts.NewMemoryStore()})
	req := httptest.NewRequest(http.MethodDelete, "/v1/workspaces/ws_demo/projects/proj_demo/workspace-bindings/wmb_demo", nil)
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("expected 500, got %d body=%s", rec.Code, rec.Body.String())
	}
	if strings.Contains(rec.Body.String(), "pvc delete failed") {
		t.Fatalf("raw pvc delete error must not leak in response, got %s", rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "delete persistent volume claim failed") {
		t.Fatalf("expected stable pvc delete error message, got %s", rec.Body.String())
	}
}

func TestDeleteBindingReturnsErrorWhenPVDeleteFails(t *testing.T) {
	client := &fakeK8sClient{
		pv:          &v1.PersistentVolume{ObjectMeta: metav1.ObjectMeta{Name: "pv"}},
		pvc:         &v1.PersistentVolumeClaim{ObjectMeta: metav1.ObjectMeta{Name: "pvc"}},
		deletePVErr: errors.New("pv delete failed"),
	}
	handler := NewHandler(client, Options{Namespace: "sandbox-workloads", WorkloadFacts: workloadfacts.NewMemoryStore()})
	req := httptest.NewRequest(http.MethodDelete, "/v1/workspaces/ws_demo/projects/proj_demo/workspace-bindings/wmb_demo", nil)
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("expected 500, got %d body=%s", rec.Code, rec.Body.String())
	}
	if strings.Contains(rec.Body.String(), "pv delete failed") {
		t.Fatalf("raw pv delete error must not leak in response, got %s", rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "delete persistent volume failed") {
		t.Fatalf("expected stable pv delete error message, got %s", rec.Body.String())
	}
}
