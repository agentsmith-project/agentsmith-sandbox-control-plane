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
	"sync"
	"testing"
	"time"

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
	pv                     *v1.PersistentVolume
	pvc                    *v1.PersistentVolumeClaim
	pods                   []v1.Pod
	listSelectors          []string
	listPodsErr            error
	deletePVErr            error
	deletePVCErr           error
	deletePVLeavesObject   bool
	deletePVCLeavesObject  bool
	deletePVCalled         bool
	deletePVCCalled        bool
	ensurePVCPhase         v1.PersistentVolumeClaimPhase
	getPVCPhases           []v1.PersistentVolumeClaimPhase
	getPVCErr              error
	getPVCErrs             []error
	getPVCCalls            int
	getPVCalls             int
	getPVAfterDeleteCalls  int
	getPVCAfterDeleteCalls int
}

type testErrorEnvelope struct {
	Error struct {
		Code      string            `json:"code"`
		Message   string            `json:"message"`
		RequestID string            `json:"request_id"`
		Details   map[string]string `json:"details"`
	} `json:"error"`
}

type memoryWorkspaceBindingReleaseStore struct {
	mu    sync.Mutex
	facts map[WorkspaceBindingReleaseKey]WorkspaceBindingReleaseFact
	err   error
}

func newMemoryWorkspaceBindingReleaseStore() *memoryWorkspaceBindingReleaseStore {
	return &memoryWorkspaceBindingReleaseStore{facts: make(map[WorkspaceBindingReleaseKey]WorkspaceBindingReleaseFact)}
}

func (s *memoryWorkspaceBindingReleaseStore) Get(_ context.Context, key WorkspaceBindingReleaseKey) (WorkspaceBindingReleaseFact, error) {
	if s.err != nil {
		return WorkspaceBindingReleaseFact{}, s.err
	}
	normalizeWorkspaceBindingReleaseKey(&key)
	s.mu.Lock()
	defer s.mu.Unlock()
	fact, ok := s.facts[key]
	if !ok {
		return WorkspaceBindingReleaseFact{}, errWorkspaceBindingReleaseFactNotFound
	}
	return fact, nil
}

func (s *memoryWorkspaceBindingReleaseStore) Save(_ context.Context, fact WorkspaceBindingReleaseFact) error {
	if s.err != nil {
		return s.err
	}
	normalizeWorkspaceBindingReleaseFact(&fact)
	s.mu.Lock()
	defer s.mu.Unlock()
	s.facts[fact.Key()] = fact
	return nil
}

func decodeErrorEnvelope(t *testing.T, rec *httptest.ResponseRecorder) testErrorEnvelope {
	t.Helper()
	var body testErrorEnvelope
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode error envelope: %v", err)
	}
	return body
}

func assertErrorDetail(t *testing.T, body testErrorEnvelope, key, want string) {
	t.Helper()
	if got := body.Error.Details[key]; got != want {
		t.Fatalf("error.details[%q] = %q, want %q; details=%#v", key, got, want, body.Error.Details)
	}
}

func assertNoSensitiveErrorDetails(t *testing.T, rec *httptest.ResponseRecorder) {
	t.Helper()
	for _, forbidden := range []string{"payload_volume_subdir", "secret_ref", "juicefs-vol-demo", "raw-secret", "p@ss"} {
		if strings.Contains(rec.Body.String(), forbidden) {
			t.Fatalf("error details leaked sensitive marker %q in %s", forbidden, rec.Body.String())
		}
	}
}

func (f *fakeK8sClient) EnsurePersistentVolume(_ context.Context, volume *v1.PersistentVolume) error {
	f.pv = volume
	return nil
}
func (f *fakeK8sClient) GetPersistentVolume(_ context.Context, _ string) (*v1.PersistentVolume, error) {
	f.getPVCalls++
	if f.deletePVCalled {
		f.getPVAfterDeleteCalls++
	}
	if f.pv == nil {
		return nil, apierrors.NewNotFound(schema.GroupResource{Resource: "persistentvolumes"}, "missing")
	}
	return f.pv, nil
}
func (f *fakeK8sClient) DeletePersistentVolume(_ context.Context, _ string) error {
	if f.deletePVErr != nil {
		return f.deletePVErr
	}
	f.deletePVCalled = true
	if f.deletePVLeavesObject {
		if f.pv != nil && f.pv.GetDeletionTimestamp() == nil {
			now := metav1.Now()
			f.pv.DeletionTimestamp = &now
		}
		return nil
	}
	f.pv = nil
	return nil
}
func (f *fakeK8sClient) EnsurePersistentVolumeClaim(_ context.Context, _ string, claim *v1.PersistentVolumeClaim) error {
	f.pvc = claim
	if f.ensurePVCPhase != "" {
		f.pvc.Status.Phase = f.ensurePVCPhase
	} else {
		f.pvc.Status.Phase = v1.ClaimBound
	}
	return nil
}
func (f *fakeK8sClient) GetPersistentVolumeClaim(_ context.Context, _ string, _ string) (*v1.PersistentVolumeClaim, error) {
	callIdx := f.getPVCCalls
	f.getPVCCalls++
	if f.deletePVCCalled {
		f.getPVCAfterDeleteCalls++
	}
	if callIdx < len(f.getPVCErrs) && f.getPVCErrs[callIdx] != nil {
		return nil, f.getPVCErrs[callIdx]
	}
	if f.getPVCErr != nil {
		return nil, f.getPVCErr
	}
	if f.pvc == nil {
		return nil, apierrors.NewNotFound(schema.GroupResource{Resource: "persistentvolumeclaims"}, "missing")
	}
	if len(f.getPVCPhases) > 0 {
		phaseIdx := callIdx
		if phaseIdx >= len(f.getPVCPhases) {
			phaseIdx = len(f.getPVCPhases) - 1
		}
		f.pvc.Status.Phase = f.getPVCPhases[phaseIdx]
	}
	return f.pvc, nil
}
func (f *fakeK8sClient) DeletePersistentVolumeClaim(_ context.Context, _ string, _ string) error {
	if f.deletePVCErr != nil {
		return f.deletePVCErr
	}
	f.deletePVCCalled = true
	if f.deletePVCLeavesObject {
		if f.pvc != nil && f.pvc.GetDeletionTimestamp() == nil {
			now := metav1.Now()
			f.pvc.DeletionTimestamp = &now
		}
		return nil
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
	plan                  afscp.OrchestratorMountPlan
	err                   error
	statusErr             error
	namespaceID           string
	mountBindingID        string
	correlationID         string
	statusNamespaceID     string
	statusMountBindingID  string
	statusValue           string
	statusReason          string
	statusCorrelationID   string
	statusIdempotencyKey  string
	statusObservedAt      time.Time
	calls                 int
	statusCalls           int
	statusNamespaceIDs    []string
	statusMountBindingIDs []string
	statusIdempotencyKeys []string
	statusObservedAts     []time.Time
	onStatus              func()
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

func (f *fakeAFSCPClient) UpdateWorkloadMountStatus(_ context.Context, namespaceID, mountBindingID, status, reason string, observedAt time.Time, correlationID, idempotencyKey string) (afscp.OperationEnvelope, error) {
	f.statusCalls++
	f.statusNamespaceID = namespaceID
	f.statusMountBindingID = mountBindingID
	f.statusValue = status
	f.statusReason = reason
	f.statusObservedAt = observedAt
	f.statusCorrelationID = correlationID
	f.statusIdempotencyKey = idempotencyKey
	f.statusNamespaceIDs = append(f.statusNamespaceIDs, namespaceID)
	f.statusMountBindingIDs = append(f.statusMountBindingIDs, mountBindingID)
	f.statusIdempotencyKeys = append(f.statusIdempotencyKeys, idempotencyKey)
	f.statusObservedAts = append(f.statusObservedAts, observedAt)
	if f.onStatus != nil {
		f.onStatus()
	}
	if f.statusErr != nil {
		return afscp.OperationEnvelope{}, f.statusErr
	}
	return afscp.OperationEnvelope{OperationID: "op_status", OperationState: "succeeded"}, nil
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

func bindingObjects(workspaceID, projectID, bindingID, namespaceID string, plan afscp.OrchestratorMountPlan) (*v1.PersistentVolume, *v1.PersistentVolumeClaim) {
	pvName, pvcName := names(workspaceID, projectID, bindingID)
	annotations := map[string]string{
		annotationWorkspaceID:              workspaceID,
		annotationProjectID:                projectID,
		annotationVolumeHandle:             volumeHandle(workspaceID, projectID, bindingID),
		annotationAFSCPNamespaceID:         namespaceID,
		annotationAFSCPMountBindingID:      plan.MountBindingID,
		annotationAFSCPVolumeID:            plan.VolumeID,
		annotationPayloadVolumeSubdir:      plan.PayloadVolumeSubdir,
		annotationMountPath:                plan.MountPath,
		annotationReadOnly:                 boolString(plan.ReadOnly),
		annotationRunAsNonRoot:             boolString(plan.SecurityPolicy.RunAsNonRoot),
		annotationAllowPrivileged:          boolString(plan.SecurityPolicy.AllowPrivileged),
		annotationJVSControlOutsidePayload: boolString(plan.SecurityPolicy.JVSControlOutsidePayload),
	}
	pv := &v1.PersistentVolume{
		ObjectMeta: metav1.ObjectMeta{
			Name:        pvName,
			Annotations: annotations,
		},
	}
	pvc := &v1.PersistentVolumeClaim{
		ObjectMeta: metav1.ObjectMeta{
			Name:        pvcName,
			Namespace:   "sandbox-workloads",
			Annotations: annotations,
		},
		Spec:   v1.PersistentVolumeClaimSpec{VolumeName: pvName},
		Status: v1.PersistentVolumeClaimStatus{Phase: v1.ClaimBound},
	}
	return pv, pvc
}

func TestRequirePVCBound(t *testing.T) {
	tests := []struct {
		name    string
		pvc     *v1.PersistentVolumeClaim
		wantErr string
	}{
		{
			name:    "nil",
			pvc:     nil,
			wantErr: "workspace binding pvc is required",
		},
		{
			name: "pending",
			pvc: &v1.PersistentVolumeClaim{
				ObjectMeta: metav1.ObjectMeta{Name: "pvc-demo"},
				Status:     v1.PersistentVolumeClaimStatus{Phase: v1.ClaimPending},
			},
			wantErr: `persistent volume claim "pvc-demo" is Pending, not Bound`,
		},
		{
			name: "bound without volume name",
			pvc: &v1.PersistentVolumeClaim{
				ObjectMeta: metav1.ObjectMeta{Name: "pvc-demo"},
				Status:     v1.PersistentVolumeClaimStatus{Phase: v1.ClaimBound},
			},
			wantErr: `persistent volume claim "pvc-demo" is Bound but has no volumeName`,
		},
		{
			name: "bound",
			pvc: &v1.PersistentVolumeClaim{
				ObjectMeta: metav1.ObjectMeta{Name: "pvc-demo"},
				Spec:       v1.PersistentVolumeClaimSpec{VolumeName: "pv-demo"},
				Status:     v1.PersistentVolumeClaimStatus{Phase: v1.ClaimBound},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := RequirePVCBound(tt.pvc)
			if tt.wantErr == "" {
				if err != nil {
					t.Fatalf("expected nil error, got %v", err)
				}
				return
			}
			if err == nil || err.Error() != tt.wantErr {
				t.Fatalf("expected error %q, got %v", tt.wantErr, err)
			}
		})
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

func TestEnsureBindingPollsUntilPVCBound(t *testing.T) {
	client := &fakeK8sClient{
		ensurePVCPhase: v1.ClaimPending,
		getPVCPhases:   []v1.PersistentVolumeClaimPhase{v1.ClaimPending, v1.ClaimBound},
	}
	handler := NewHandler(client, Options{
		Namespace:        "sandbox-workloads",
		CSIDriver:        "csi.juicefs.com",
		StorageCapacity:  "1Pi",
		StorageClassName: "juicefs-static",
		AFSCPClient:      &fakeAFSCPClient{plan: validPlan()},
	})

	payload := `{"namespace_id":"ns_demo","mount_binding_id":"wmb_demo"}`
	req := httptest.NewRequest(http.MethodPut, "/v1/workspaces/ws_demo/projects/proj_demo/workspace-bindings/wmb_demo", strings.NewReader(payload))
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d body=%s", rec.Code, rec.Body.String())
	}
	if client.getPVCCalls != 2 {
		t.Fatalf("expected two PVC reads before Bound, got %d", client.getPVCCalls)
	}
	var status BindingStatus
	if err := json.Unmarshal(rec.Body.Bytes(), &status); err != nil {
		t.Fatalf("unmarshal status: %v", err)
	}
	if status.Status != "ready" || status.PVCName == "" {
		t.Fatalf("unexpected binding status after PVC bound poll: %+v", status)
	}
}

func TestEnsureBindingPollsThroughTransientPVCNotFoundUntilBound(t *testing.T) {
	client := &fakeK8sClient{
		getPVCErrs: []error{
			apierrors.NewNotFound(schema.GroupResource{Resource: "persistentvolumeclaims"}, "pvc-ws-demo-proj-demo-wmb-demo"),
		},
	}
	handler := NewHandler(client, Options{
		Namespace:        "sandbox-workloads",
		CSIDriver:        "csi.juicefs.com",
		StorageCapacity:  "1Pi",
		StorageClassName: "juicefs-static",
		AFSCPClient:      &fakeAFSCPClient{plan: validPlan()},
	})

	payload := `{"namespace_id":"ns_demo","mount_binding_id":"wmb_demo"}`
	req := httptest.NewRequest(http.MethodPut, "/v1/workspaces/ws_demo/projects/proj_demo/workspace-bindings/wmb_demo", strings.NewReader(payload))
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d body=%s", rec.Code, rec.Body.String())
	}
	if client.getPVCCalls != 2 {
		t.Fatalf("expected one NotFound read and one Bound read, got %d", client.getPVCCalls)
	}
	var status BindingStatus
	if err := json.Unmarshal(rec.Body.Bytes(), &status); err != nil {
		t.Fatalf("unmarshal status: %v", err)
	}
	if status.Status != "ready" || status.PVCName == "" {
		t.Fatalf("unexpected binding status after transient PVC NotFound: %+v", status)
	}
}

func TestEnsureBindingReturnsNotReadyWhenPVCBoundPollTimesOut(t *testing.T) {
	client := &fakeK8sClient{ensurePVCPhase: v1.ClaimPending}
	handler := NewHandler(client, Options{
		Namespace:        "sandbox-workloads",
		CSIDriver:        "csi.juicefs.com",
		StorageCapacity:  "1Pi",
		StorageClassName: "juicefs-static",
		AFSCPClient:      &fakeAFSCPClient{plan: validPlan()},
	})

	payload := `{"namespace_id":"ns_demo","mount_binding_id":"wmb_demo"}`
	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Millisecond)
	defer cancel()
	req := httptest.NewRequestWithContext(ctx, http.MethodPut, "/v1/workspaces/ws_demo/projects/proj_demo/workspace-bindings/wmb_demo", strings.NewReader(payload))
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("expected 503, got %d body=%s", rec.Code, rec.Body.String())
	}
	body := decodeErrorEnvelope(t, rec)
	if body.Error.Code != "not_ready" {
		t.Fatalf("expected not_ready code, got %+v", body.Error)
	}
	if rec.Header().Get("Retry-After") != ensurePVCBoundRetryAfter {
		t.Fatalf("expected Retry-After %q, got %q", ensurePVCBoundRetryAfter, rec.Header().Get("Retry-After"))
	}
	if !strings.Contains(body.Error.Message, "persistent volume claim") || !strings.Contains(body.Error.Message, "Pending") {
		t.Fatalf("expected PVC pending not-ready message, got %+v", body.Error)
	}
	assertErrorDetail(t, body, "operation", "workspace_binding.ensure")
	assertErrorDetail(t, body, "workspace_id", "ws_demo")
	assertErrorDetail(t, body, "project_id", "proj_demo")
	assertErrorDetail(t, body, "binding_id", "wmb_demo")
	assertErrorDetail(t, body, "resource", "persistent_volume_claim")
	assertErrorDetail(t, body, "reason", "pvc_unbound")
	assertErrorDetail(t, body, "phase", "Pending")
	assertErrorDetail(t, body, "status", "not_ready")
	assertErrorDetail(t, body, "stable_code", "not_ready")
	assertErrorDetail(t, body, "retry_after", ensurePVCBoundRetryAfter)
	assertNoSensitiveErrorDetails(t, rec)
	if client.pv == nil || client.pvc == nil {
		t.Fatalf("ensure should still create PV/PVC before reporting binding not ready")
	}
	if client.getPVCCalls < 2 {
		t.Fatalf("expected bounded poll to read PVC more than once, got %d", client.getPVCCalls)
	}
}

func TestEnsureBindingReturnsNotReadyWhenPVCNotFoundPollTimesOut(t *testing.T) {
	client := &fakeK8sClient{
		getPVCErr: apierrors.NewNotFound(schema.GroupResource{Resource: "persistentvolumeclaims"}, "pvc-ws-demo-proj-demo-wmb-demo"),
	}
	handler := NewHandler(client, Options{
		Namespace:        "sandbox-workloads",
		CSIDriver:        "csi.juicefs.com",
		StorageCapacity:  "1Pi",
		StorageClassName: "juicefs-static",
		AFSCPClient:      &fakeAFSCPClient{plan: validPlan()},
	})

	payload := `{"namespace_id":"ns_demo","mount_binding_id":"wmb_demo"}`
	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Millisecond)
	defer cancel()
	req := httptest.NewRequestWithContext(ctx, http.MethodPut, "/v1/workspaces/ws_demo/projects/proj_demo/workspace-bindings/wmb_demo", strings.NewReader(payload))
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("expected 503, got %d body=%s", rec.Code, rec.Body.String())
	}
	body := decodeErrorEnvelope(t, rec)
	if body.Error.Code != "not_ready" {
		t.Fatalf("expected not_ready code, got %+v", body.Error)
	}
	if rec.Header().Get("Retry-After") != ensurePVCBoundRetryAfter {
		t.Fatalf("expected Retry-After %q, got %q", ensurePVCBoundRetryAfter, rec.Header().Get("Retry-After"))
	}
	if !strings.Contains(body.Error.Message, "not visible yet") {
		t.Fatalf("expected PVC not-visible not-ready message, got %+v", body.Error)
	}
	assertErrorDetail(t, body, "operation", "workspace_binding.ensure")
	assertErrorDetail(t, body, "workspace_id", "ws_demo")
	assertErrorDetail(t, body, "project_id", "proj_demo")
	assertErrorDetail(t, body, "binding_id", "wmb_demo")
	assertErrorDetail(t, body, "resource", "persistent_volume_claim")
	assertErrorDetail(t, body, "reason", "pvc_missing")
	assertErrorDetail(t, body, "phase", "missing")
	assertErrorDetail(t, body, "status", "not_ready")
	assertErrorDetail(t, body, "stable_code", "not_ready")
	assertErrorDetail(t, body, "retry_after", ensurePVCBoundRetryAfter)
	assertNoSensitiveErrorDetails(t, rec)
	if client.pv == nil || client.pvc == nil {
		t.Fatalf("ensure should still create PV/PVC before reporting binding not ready")
	}
	if client.getPVCCalls < 2 {
		t.Fatalf("expected bounded poll to retry NotFound PVC reads, got %d", client.getPVCCalls)
	}
}

func TestEnsureBindingFailsFastWhenPVCGetForbidden(t *testing.T) {
	client := &fakeK8sClient{
		getPVCErr: apierrors.NewForbidden(schema.GroupResource{Resource: "persistentvolumeclaims"}, "pvc-ws-demo-proj-demo-wmb-demo", errors.New("rbac denied")),
	}
	handler := NewHandler(client, Options{
		Namespace:        "sandbox-workloads",
		CSIDriver:        "csi.juicefs.com",
		StorageCapacity:  "1Pi",
		StorageClassName: "juicefs-static",
		AFSCPClient:      &fakeAFSCPClient{plan: validPlan()},
	})

	payload := `{"namespace_id":"ns_demo","mount_binding_id":"wmb_demo"}`
	req := httptest.NewRequest(http.MethodPut, "/v1/workspaces/ws_demo/projects/proj_demo/workspace-bindings/wmb_demo", strings.NewReader(payload))
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("expected 500, got %d body=%s", rec.Code, rec.Body.String())
	}
	body := decodeErrorEnvelope(t, rec)
	if body.Error.Code != "internal_error" || body.Error.Message != "get persistent volume claim failed" {
		t.Fatalf("expected sanitized internal_error, got %+v", body.Error)
	}
	if rec.Header().Get("Retry-After") != "" {
		t.Fatalf("Forbidden PVC Get must not be exposed as readiness retry, got Retry-After=%q", rec.Header().Get("Retry-After"))
	}
	if client.getPVCCalls != 1 {
		t.Fatalf("expected Forbidden PVC Get to fail fast after one read, got %d", client.getPVCCalls)
	}
}

func TestEnsureBindingFailsFastWhenPVCGetReturnsGenericError(t *testing.T) {
	client := &fakeK8sClient{
		getPVCErr: errors.New("apiserver unavailable"),
	}
	handler := NewHandler(client, Options{
		Namespace:        "sandbox-workloads",
		CSIDriver:        "csi.juicefs.com",
		StorageCapacity:  "1Pi",
		StorageClassName: "juicefs-static",
		AFSCPClient:      &fakeAFSCPClient{plan: validPlan()},
	})

	payload := `{"namespace_id":"ns_demo","mount_binding_id":"wmb_demo"}`
	req := httptest.NewRequest(http.MethodPut, "/v1/workspaces/ws_demo/projects/proj_demo/workspace-bindings/wmb_demo", strings.NewReader(payload))
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("expected 500, got %d body=%s", rec.Code, rec.Body.String())
	}
	body := decodeErrorEnvelope(t, rec)
	if body.Error.Code != "internal_error" || body.Error.Message != "get persistent volume claim failed" {
		t.Fatalf("expected sanitized internal_error, got %+v", body.Error)
	}
	if rec.Header().Get("Retry-After") != "" {
		t.Fatalf("generic PVC Get error must not be exposed as readiness retry, got Retry-After=%q", rec.Header().Get("Retry-After"))
	}
	if client.getPVCCalls != 1 {
		t.Fatalf("expected generic PVC Get error to fail fast after one read, got %d", client.getPVCCalls)
	}
}

func TestGetBindingReturnsNotReadyWhenPVCUnbound(t *testing.T) {
	plan := validPlan()
	handler := NewHandler(&fakeK8sClient{
		pv: &v1.PersistentVolume{
			ObjectMeta: metav1.ObjectMeta{Name: PVName("ws_demo", "proj_demo", "wmb_demo")},
			Spec: v1.PersistentVolumeSpec{
				MountOptions: []string{"subdir=" + plan.PayloadVolumeSubdir},
				PersistentVolumeSource: v1.PersistentVolumeSource{
					CSI: &v1.CSIPersistentVolumeSource{},
				},
			},
		},
		pvc: &v1.PersistentVolumeClaim{
			ObjectMeta: metav1.ObjectMeta{
				Name: PVCName("ws_demo", "proj_demo", "wmb_demo"),
				Annotations: map[string]string{
					annotationAFSCPNamespaceID:         "ns_demo",
					annotationAFSCPMountBindingID:      "wmb_demo",
					annotationAFSCPVolumeID:            plan.VolumeID,
					annotationPayloadVolumeSubdir:      plan.PayloadVolumeSubdir,
					annotationMountPath:                plan.MountPath,
					annotationReadOnly:                 boolString(plan.ReadOnly),
					annotationRunAsNonRoot:             "true",
					annotationAllowPrivileged:          "false",
					annotationJVSControlOutsidePayload: "true",
				},
			},
			Spec:   v1.PersistentVolumeClaimSpec{VolumeName: PVName("ws_demo", "proj_demo", "wmb_demo")},
			Status: v1.PersistentVolumeClaimStatus{Phase: v1.ClaimPending},
		},
	}, Options{Namespace: "sandbox-workloads"})

	req := httptest.NewRequest(http.MethodGet, "/v1/workspaces/ws_demo/projects/proj_demo/workspace-bindings/wmb_demo", nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("expected 503, got %d body=%s", rec.Code, rec.Body.String())
	}
	body := decodeErrorEnvelope(t, rec)
	if body.Error.Code != "not_ready" {
		t.Fatalf("expected not_ready code, got %+v", body.Error)
	}
	assertErrorDetail(t, body, "operation", "workspace_binding.get")
	assertErrorDetail(t, body, "workspace_id", "ws_demo")
	assertErrorDetail(t, body, "project_id", "proj_demo")
	assertErrorDetail(t, body, "binding_id", "wmb_demo")
	assertErrorDetail(t, body, "resource", "persistent_volume_claim")
	assertErrorDetail(t, body, "reason", "pvc_unbound")
	assertErrorDetail(t, body, "phase", "Pending")
	assertErrorDetail(t, body, "status", "not_ready")
	assertErrorDetail(t, body, "stable_code", "not_ready")
	assertNoSensitiveErrorDetails(t, rec)
}

func TestGetBindingReturnsNotReadyWhenPVExistsButPVCNotFound(t *testing.T) {
	plan := validPlan()
	handler := NewHandler(&fakeK8sClient{
		pv: &v1.PersistentVolume{
			ObjectMeta: metav1.ObjectMeta{Name: PVName("ws_demo", "proj_demo", "wmb_demo")},
			Spec: v1.PersistentVolumeSpec{
				MountOptions: []string{"subdir=" + plan.PayloadVolumeSubdir},
				PersistentVolumeSource: v1.PersistentVolumeSource{
					CSI: &v1.CSIPersistentVolumeSource{},
				},
			},
		},
	}, Options{Namespace: "sandbox-workloads"})

	req := httptest.NewRequest(http.MethodGet, "/v1/workspaces/ws_demo/projects/proj_demo/workspace-bindings/wmb_demo", nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("expected 503, got %d body=%s", rec.Code, rec.Body.String())
	}
	body := decodeErrorEnvelope(t, rec)
	if body.Error.Code != "not_ready" {
		t.Fatalf("expected not_ready code, got %+v", body.Error)
	}
	if !strings.Contains(body.Error.Message, "not visible yet") {
		t.Fatalf("expected PVC not-visible not-ready message, got %+v", body.Error)
	}
	assertErrorDetail(t, body, "operation", "workspace_binding.get")
	assertErrorDetail(t, body, "workspace_id", "ws_demo")
	assertErrorDetail(t, body, "project_id", "proj_demo")
	assertErrorDetail(t, body, "binding_id", "wmb_demo")
	assertErrorDetail(t, body, "resource", "persistent_volume_claim")
	assertErrorDetail(t, body, "reason", "pvc_missing")
	assertErrorDetail(t, body, "phase", "missing")
	assertErrorDetail(t, body, "status", "not_ready")
	assertErrorDetail(t, body, "stable_code", "not_ready")
	assertNoSensitiveErrorDetails(t, rec)
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

func TestDeleteBindingWithNoActiveWorkloadsMarksAFSCPTerminalReleased(t *testing.T) {
	plan := validPlan()
	pv, pvc := bindingObjects("ws_demo", "proj_demo", "wmb_demo", "ns_demo", plan)
	client := &fakeK8sClient{pv: pv, pvc: pvc}
	afscpClient := &fakeAFSCPClient{plan: plan}
	afscpClient.onStatus = func() {
		if client.getPVAfterDeleteCalls == 0 || client.getPVCAfterDeleteCalls == 0 {
			t.Fatalf("AFSCP released status must wait for PV/PVC NotFound boundary; post-delete reads: pv=%d pvc=%d", client.getPVAfterDeleteCalls, client.getPVCAfterDeleteCalls)
		}
	}
	releaseFacts := newMemoryWorkspaceBindingReleaseStore()
	handler := NewHandler(client, Options{
		Namespace:     "sandbox-workloads",
		WorkloadFacts: workloadfacts.NewMemoryStore(),
		AFSCPClient:   afscpClient,
		ReleaseFacts:  releaseFacts,
	})
	req := httptest.NewRequest(http.MethodDelete, "/v1/workspaces/ws_demo/projects/proj_demo/workspace-bindings/wmb_demo", nil)
	req.Header.Set("X-Correlation-Id", "corr-delete-binding")
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d body=%s", rec.Code, rec.Body.String())
	}
	if client.pv != nil || client.pvc != nil {
		t.Fatalf("expected resources to be deleted before terminal status is reported")
	}
	if afscpClient.statusCalls != 1 {
		t.Fatalf("expected one AFSCP status call, got %d", afscpClient.statusCalls)
	}
	if afscpClient.statusNamespaceID != "ns_demo" || afscpClient.statusMountBindingID != "wmb_demo" {
		t.Fatalf("unexpected AFSCP status ref: namespace=%q mount=%q", afscpClient.statusNamespaceID, afscpClient.statusMountBindingID)
	}
	if afscpClient.statusValue != "released" || afscpClient.statusReason != "workspace binding deleted" {
		t.Fatalf("unexpected AFSCP status: value=%q reason=%q", afscpClient.statusValue, afscpClient.statusReason)
	}
	if afscpClient.statusCorrelationID != "corr-delete-binding" {
		t.Fatalf("unexpected correlation id %q", afscpClient.statusCorrelationID)
	}
	if afscpClient.statusObservedAt.IsZero() {
		t.Fatalf("expected observed_at to be set")
	}
	wantKey := workspaceBindingStatusIdempotencyKey("ws_demo", "proj_demo", "wmb_demo", workspaceBindingMountRef{namespaceID: "ns_demo", mountBindingID: "wmb_demo"})
	if afscpClient.statusIdempotencyKey != wantKey {
		t.Fatalf("unexpected idempotency key %q, want %q", afscpClient.statusIdempotencyKey, wantKey)
	}
	fact, err := releaseFacts.Get(context.Background(), WorkspaceBindingReleaseKey{WorkspaceID: "ws_demo", ProjectID: "proj_demo", BindingID: "wmb_demo"})
	if err != nil {
		t.Fatalf("get release fact: %v", err)
	}
	if !fact.Complete() {
		t.Fatalf("expected complete release fact, got %+v", fact)
	}
}

func TestDeleteBindingAFSCPTerminalPendingReturnsReleaseIncomplete(t *testing.T) {
	plan := validPlan()
	pv, pvc := bindingObjects("ws_demo", "proj_demo", "wmb_demo", "ns_demo", plan)
	client := &fakeK8sClient{pv: pv, pvc: pvc}
	releaseFacts := newMemoryWorkspaceBindingReleaseStore()
	afscpClient := &fakeAFSCPClient{
		plan: plan,
		statusErr: &afscp.PendingOperationError{
			OperationID:    "op_binding_status_pending",
			OperationState: "running",
			RequestID:      "afscp-req-binding",
			Code:           "OPERATION_PENDING",
			Retryable:      true,
		},
	}
	handler := NewHandler(client, Options{
		Namespace:     "sandbox-workloads",
		WorkloadFacts: workloadfacts.NewMemoryStore(),
		AFSCPClient:   afscpClient,
		ReleaseFacts:  releaseFacts,
	})
	req := httptest.NewRequest(http.MethodDelete, "/v1/workspaces/ws_demo/projects/proj_demo/workspace-bindings/wmb_demo", nil)
	req.Header.Set("X-Correlation-Id", "corr-delete-binding")
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusConflict {
		t.Fatalf("expected 409, got %d body=%s", rec.Code, rec.Body.String())
	}
	if rec.Header().Get("Retry-After") != workspaceBindingReleaseRetryAfter {
		t.Fatalf("expected Retry-After %q, got %q", workspaceBindingReleaseRetryAfter, rec.Header().Get("Retry-After"))
	}
	body := decodeErrorEnvelope(t, rec)
	if body.Error.Code != "workspace_binding_release_incomplete" {
		t.Fatalf("expected stable release-incomplete code, got %+v", body.Error)
	}
	assertErrorDetail(t, body, "operation", "workspace_binding.delete")
	assertErrorDetail(t, body, "workspace_id", "ws_demo")
	assertErrorDetail(t, body, "project_id", "proj_demo")
	assertErrorDetail(t, body, "binding_id", "wmb_demo")
	assertErrorDetail(t, body, "resource", "afscp_workload_mount_binding")
	assertErrorDetail(t, body, "reason", "afscp_operation_pending")
	assertErrorDetail(t, body, "phase", "running")
	assertErrorDetail(t, body, "status", "released_status_pending")
	assertErrorDetail(t, body, "stable_code", "workspace_binding_release_incomplete")
	assertErrorDetail(t, body, "dependency_operation_id", "op_binding_status_pending")
	assertErrorDetail(t, body, "dependency_request_id", "afscp-req-binding")
	assertErrorDetail(t, body, "dependency_code", "OPERATION_PENDING")
	assertErrorDetail(t, body, "dependency_state", "running")
	if client.pv != nil || client.pvc != nil {
		t.Fatalf("PV/PVC deletion boundary must converge before reporting AFSCP terminal pending")
	}
	if afscpClient.statusCalls != 1 || afscpClient.statusValue != "released" {
		t.Fatalf("expected one released status attempt, got calls=%d status=%q", afscpClient.statusCalls, afscpClient.statusValue)
	}
	assertNoSensitiveErrorDetails(t, rec)

	afscpClient.statusErr = nil
	retryReq := httptest.NewRequest(http.MethodDelete, "/v1/workspaces/ws_demo/projects/proj_demo/workspace-bindings/wmb_demo", nil)
	retryReq.Header.Set("X-Correlation-Id", "corr-delete-binding-retry")
	retryRec := httptest.NewRecorder()

	handler.ServeHTTP(retryRec, retryReq)

	if retryRec.Code != http.StatusOK {
		t.Fatalf("expected retry to complete with preserved mount ref, got %d body=%s", retryRec.Code, retryRec.Body.String())
	}
	if afscpClient.statusCalls != 2 {
		t.Fatalf("expected second AFSCP status retry, got %d calls", afscpClient.statusCalls)
	}
	if len(afscpClient.statusNamespaceIDs) != 2 || afscpClient.statusNamespaceIDs[0] != "ns_demo" || afscpClient.statusNamespaceIDs[1] != "ns_demo" {
		t.Fatalf("retry lost namespace ref: %#v", afscpClient.statusNamespaceIDs)
	}
	if len(afscpClient.statusMountBindingIDs) != 2 || afscpClient.statusMountBindingIDs[0] != "wmb_demo" || afscpClient.statusMountBindingIDs[1] != "wmb_demo" {
		t.Fatalf("retry lost mount binding ref: %#v", afscpClient.statusMountBindingIDs)
	}
	if len(afscpClient.statusIdempotencyKeys) != 2 || afscpClient.statusIdempotencyKeys[0] != afscpClient.statusIdempotencyKeys[1] {
		t.Fatalf("retry must use the same idempotency key, got %#v", afscpClient.statusIdempotencyKeys)
	}
	if len(afscpClient.statusObservedAts) != 2 || !afscpClient.statusObservedAts[0].Equal(afscpClient.statusObservedAts[1]) {
		t.Fatalf("retry must use the same observed_at, got %#v", afscpClient.statusObservedAts)
	}
}

func TestDeleteBindingStorageDeletionPendingReturnsReleaseIncompleteBeforeAFSCPStatus(t *testing.T) {
	plan := validPlan()
	pv, pvc := bindingObjects("ws_demo", "proj_demo", "wmb_demo", "ns_demo", plan)
	client := &fakeK8sClient{
		pv:                    pv,
		pvc:                   pvc,
		deletePVLeavesObject:  true,
		deletePVCLeavesObject: true,
	}
	afscpClient := &fakeAFSCPClient{plan: plan}
	handler := NewHandler(client, Options{
		Namespace:     "sandbox-workloads",
		WorkloadFacts: workloadfacts.NewMemoryStore(),
		AFSCPClient:   afscpClient,
		ReleaseFacts:  newMemoryWorkspaceBindingReleaseStore(),
	})
	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Millisecond)
	defer cancel()
	req := httptest.NewRequestWithContext(ctx, http.MethodDelete, "/v1/workspaces/ws_demo/projects/proj_demo/workspace-bindings/wmb_demo", nil)
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusConflict {
		t.Fatalf("expected 409, got %d body=%s", rec.Code, rec.Body.String())
	}
	if rec.Header().Get("Retry-After") != workspaceBindingReleaseRetryAfter {
		t.Fatalf("expected Retry-After %q, got %q", workspaceBindingReleaseRetryAfter, rec.Header().Get("Retry-After"))
	}
	body := decodeErrorEnvelope(t, rec)
	if body.Error.Code != "workspace_binding_release_incomplete" {
		t.Fatalf("expected stable release-incomplete code, got %+v", body.Error)
	}
	assertErrorDetail(t, body, "operation", "workspace_binding.delete")
	assertErrorDetail(t, body, "resource", "persistent_volume_binding")
	assertErrorDetail(t, body, "reason", "storage_deletion_pending")
	assertErrorDetail(t, body, "status", "storage_deletion_pending")
	assertErrorDetail(t, body, "stable_code", "workspace_binding_release_incomplete")
	if afscpClient.statusCalls != 0 {
		t.Fatalf("AFSCP released status must not be written before storage deletion boundary, got %d calls", afscpClient.statusCalls)
	}
	if client.pv == nil || client.pvc == nil {
		t.Fatalf("test setup expected delete to be accepted while objects remain")
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
