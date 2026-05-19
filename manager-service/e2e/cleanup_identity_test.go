package e2e_test

import (
	"strings"
	"testing"

	v1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func TestCleanupIdentityFromPodPrefersRawAnnotations(t *testing.T) {
	pod := v1.Pod{ObjectMeta: metav1.ObjectMeta{
		Labels: map[string]string{
			"workspace_id": "workspace-safe-aaaaaaaaaaaa",
			"project_id":   "project-safe-bbbbbbbbbbbb",
			"workload_id":  "workload-safe-cccccccccccc",
		},
		Annotations: map[string]string{
			"mbos.io/workspace-id": "Workspace_Original",
			"mbos.io/project-id":   "Project_Original",
			"mbos.io/workload-id":  "Workload_Original",
		},
	}}

	wsID, projID, wlID, ok := cleanupIdentityFromPod(pod)

	if !ok {
		t.Fatalf("expected cleanup identity from annotations")
	}
	if wsID != "Workspace_Original" || projID != "Project_Original" || wlID != "Workload_Original" {
		t.Fatalf("cleanup identity = (%q, %q, %q), want raw annotation IDs", wsID, projID, wlID)
	}
}

func TestCleanupIdentityFromPodFallsBackToLegacyRawLabels(t *testing.T) {
	pod := v1.Pod{ObjectMeta: metav1.ObjectMeta{
		Labels: map[string]string{
			"workspace_id": "legacy_ws",
			"project_id":   "legacy_proj",
			"workload_id":  "legacy_wl",
		},
	}}

	wsID, projID, wlID, ok := cleanupIdentityFromPod(pod)

	if !ok {
		t.Fatalf("expected cleanup identity from legacy labels")
	}
	if wsID != "legacy_ws" || projID != "legacy_proj" || wlID != "legacy_wl" {
		t.Fatalf("cleanup identity = (%q, %q, %q), want legacy label IDs", wsID, projID, wlID)
	}
}

func TestCleanupIdentityFromPodDoesNotTreatHashSafeLabelsAsRawIDs(t *testing.T) {
	pod := v1.Pod{ObjectMeta: metav1.ObjectMeta{
		Labels: map[string]string{
			"workspace_id": "workspace-safe-aaaaaaaaaaaa",
			"project_id":   "project-safe-bbbbbbbbbbbb",
			"workload_id":  "workload-safe-cccccccccccc",
		},
	}}

	_, _, _, ok := cleanupIdentityFromPod(pod)

	if ok {
		t.Fatalf("hash-safe labels without raw annotations must not be treated as original IDs")
	}
}

func cleanupIdentityFromPod(pod v1.Pod) (workspaceID, projectID, workloadID string, ok bool) {
	annotations := pod.GetAnnotations()
	labels := pod.GetLabels()

	workspaceID = cleanupIdentityValue(annotations["mbos.io/workspace-id"], labels["workspace_id"])
	projectID = cleanupIdentityValue(annotations["mbos.io/project-id"], labels["project_id"])
	workloadID = cleanupIdentityValue(annotations["mbos.io/workload-id"], labels["workload_id"])

	ok = workspaceID != "" && projectID != "" && workloadID != ""
	return
}

func cleanupIdentityValue(annotationValue, labelValue string) string {
	if value := strings.TrimSpace(annotationValue); value != "" {
		return value
	}
	return legacyRawIdentityLabel(labelValue)
}

func legacyRawIdentityLabel(value string) string {
	value = strings.TrimSpace(value)
	if value == "" || looksHashSafeIdentityLabel(value) {
		return ""
	}
	return value
}

func looksHashSafeIdentityLabel(value string) bool {
	idx := strings.LastIndex(value, "-")
	if idx <= 0 || len(value)-idx-1 != 12 {
		return false
	}
	for _, r := range value[idx+1:] {
		if (r < '0' || r > '9') && (r < 'a' || r > 'f') {
			return false
		}
	}
	return true
}

func assertDNSSafeIdentityLabel(t testing.TB, labels map[string]string, key string) {
	t.Helper()
	value := strings.TrimSpace(labels[key])
	if !isDNSLabelValue(value) {
		t.Fatalf("label %s=%q must be a non-empty DNS-safe identity label", key, value)
	}
}

func isDNSLabelValue(value string) bool {
	if value == "" || len(value) > 63 {
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
