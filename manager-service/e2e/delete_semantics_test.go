package e2e_test

import (
	"net/http"
	"testing"
)

func isConfirmedWorkloadDeleteStatus(status int) bool {
	return status == http.StatusOK
}

func isTestCleanupOnlyWorkloadDeleteTolerated(status int) bool {
	// Test cleanup may race with another cleanup path. This does not weaken the
	// product DELETE contract: only a confirmed terminal release status is success.
	return isConfirmedWorkloadDeleteStatus(status) || status == http.StatusNotFound
}

func TestCleanupDeleteStatusClassification(t *testing.T) {
	if !isConfirmedWorkloadDeleteStatus(http.StatusOK) {
		t.Fatalf("200 must be the only confirmed workload DELETE success status")
	}
	if isConfirmedWorkloadDeleteStatus(http.StatusNotFound) {
		t.Fatalf("404 must not be classified as confirmed workload DELETE success")
	}
	if isConfirmedWorkloadDeleteStatus(http.StatusConflict) {
		t.Fatalf("409 must not be classified as confirmed workload DELETE success")
	}
	if !isTestCleanupOnlyWorkloadDeleteTolerated(http.StatusNotFound) {
		t.Fatalf("404 may only be tolerated by best-effort test cleanup")
	}
	if isTestCleanupOnlyWorkloadDeleteTolerated(http.StatusConflict) {
		t.Fatalf("409 must remain a retry/reconcile signal, even during cleanup")
	}
}
