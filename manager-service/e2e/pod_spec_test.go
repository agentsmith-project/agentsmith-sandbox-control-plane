//go:build e2e

package e2e_test

// pod_spec_test.go – Validates the Kubernetes Pod spec produced by ASBCP.
//
// Each test directly inspects the K8s Pod object via the API, verifying that:
//   - Labels match the API contract (workload_id, workspace_id, project_id, app)
//   - Annotations carry the lifecycle metadata (expires_at, last_activity_at, timeouts)
//   - Pod-level SecurityContext enforces non-root execution with correct UID/GID
//   - Container-level SecurityContext complies with K8s "restricted" PSS
//   - Workspace init container also runs non-root and complies with restricted PSS
//   - PVC volume mount uses the mount path from the AFSCP plan
//   - Container working directory is the plan mount path plus /workspace
//   - RestartPolicy=Never and AutomountServiceAccountToken=false are set
//   - Environment variables include TASK_HOME, HOME, WORKSPACE_PATH + user-supplied vars
//   - Resource requests/limits are applied exactly as requested

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"testing"
	"time"

	"github.com/agentsmith-project/agentsmith-sandbox-control-plane/internal/workspacebinding"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	v1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// ---------------------------------------------------------------------------
// Helpers
// ---------------------------------------------------------------------------

// fetchPod retrieves the Pod object from Kubernetes.
func fetchPod(t *testing.T, podName string) *v1.Pod {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	pod, err := k8sCli.CoreV1().Pods(suite.Namespace).Get(ctx, podName, metav1.GetOptions{})
	require.NoError(t, err, "get pod %s: %v", podName, err)
	return pod
}

// setupPod creates a workload and returns the Pod object for spec inspection.
// mustCreateWorkload already waits for the pod to be Ready, so we only need
// waitPodExists (the pod is guaranteed to be present and Running by this point).
func setupPod(t *testing.T, prefix string, req CreateRequest) (wlID string, pod *v1.Pod) {
	t.Helper()
	wlID = uniqueID(prefix)
	_ = mustCreateWorkload(t, testWS, testProj, wlID, req)
	t.Cleanup(func() { newClient().DeleteWorkload(t, testWS, testProj, wlID) })
	podName := workloadPodName(testWS, testProj, wlID)
	waitPodExists(t, suite.Namespace, podName, 10*time.Second)
	return wlID, fetchPod(t, podName)
}

func startCreateWorkloadRequest(ctx context.Context, wsID, projID, wlID string, req CreateRequest) <-chan error {
	done := make(chan error, 1)
	go func() {
		c := newClient()
		httpReq, err := http.NewRequestWithContext(ctx, http.MethodPut, c.workloadURL(wsID, projID, wlID), jsonBody(req))
		if err != nil {
			done <- err
			return
		}
		httpReq.Header.Set("Content-Type", "application/json")
		httpReq.Header.Set("X-Service-Key", c.serviceKey)

		resp, err := c.http.Do(httpReq)
		if err != nil {
			if ctx.Err() != nil {
				done <- nil
				return
			}
			done <- err
			return
		}
		defer resp.Body.Close()

		body, readErr := io.ReadAll(resp.Body)
		if readErr != nil {
			done <- readErr
			return
		}
		if resp.StatusCode != http.StatusCreated && resp.StatusCode != http.StatusOK {
			done <- fmt.Errorf("create workload returned %d: %s", resp.StatusCode, string(body))
			return
		}
		done <- nil
	}()
	return done
}

// ---------------------------------------------------------------------------
// Labels
// ---------------------------------------------------------------------------

// TestPodSpec_Labels verifies labels carry DNS-safe identity and raw IDs stay in annotations.
func TestPodSpec_Labels(t *testing.T) {
	wlID, pod := setupPod(t, "spec-labels", CreateRequest{Image: suite.Image})

	labels := pod.Labels
	assert.Equal(t, "managed-workload", labels["app"], "app label must be 'managed-workload'")
	assertDNSSafeIdentityLabel(t, labels, "workload_id")
	assertDNSSafeIdentityLabel(t, labels, "workspace_id")
	assertDNSSafeIdentityLabel(t, labels, "project_id")

	annotations := pod.Annotations
	assert.Equal(t, wlID, annotations["mbos.io/workload-id"], "raw workload ID must be retained in annotations")
	assert.Equal(t, testWS, annotations["mbos.io/workspace-id"], "raw workspace ID must be retained in annotations")
	assert.Equal(t, testProj, annotations["mbos.io/project-id"], "raw project ID must be retained in annotations")
}

// ---------------------------------------------------------------------------
// Annotations
// ---------------------------------------------------------------------------

// TestPodSpec_AnnotationsPresent verifies all lifecycle annotations are present
// and parseable immediately after pod creation.
func TestPodSpec_AnnotationsPresent(t *testing.T) {
	_, pod := setupPod(t, "spec-ann", CreateRequest{
		Image:          suite.Image,
		IdleTimeoutSec: 600,
		MaxLifetimeSec: 3600,
	})

	ann := pod.Annotations
	requiredAnnotations := []string{
		"expires_at",
		"last_activity_at",
		"workload/idleTimeoutSec",
		"workload/maxLifetimeSec",
		"workload/maxExpiresAt",
	}
	for _, key := range requiredAnnotations {
		assert.NotEmpty(t, ann[key], "annotation %q must be present and non-empty", key)
	}

	// Validate RFC3339 format on time annotations.
	for _, key := range []string{"expires_at", "last_activity_at", "workload/maxExpiresAt"} {
		_, err := time.Parse(time.RFC3339, ann[key])
		assert.NoError(t, err, "annotation %q must be valid RFC3339: %q", key, ann[key])
	}

	assert.Equal(t, "600", ann["workload/idleTimeoutSec"])
	assert.Equal(t, "3600", ann["workload/maxLifetimeSec"])
}

// TestPodSpec_AnnotationsDefaultTimeouts verifies default idle/max-lifetime
// annotations are set when the request does not specify custom values.
func TestPodSpec_AnnotationsDefaultTimeouts(t *testing.T) {
	_, pod := setupPod(t, "spec-ann-defaults", CreateRequest{Image: suite.Image})

	ann := pod.Annotations
	assert.Equal(t, fmt.Sprintf("%d", int(DefaultIdleTimeout.Seconds())),
		ann["workload/idleTimeoutSec"], "default idle timeout must be 30 minutes")
	assert.Equal(t, "86400", ann["workload/maxLifetimeSec"],
		"default max lifetime must be 24 hours (86400s)")
}

// ---------------------------------------------------------------------------
// Pod-level SecurityContext
// ---------------------------------------------------------------------------

// TestPodSpec_PodSecurityContext verifies the pod-level security context enforces
// non-root execution with UID/GID 1000 and the required fsGroup settings.
func TestPodSpec_PodSecurityContext(t *testing.T) {
	_, pod := setupPod(t, "spec-pod-sc", CreateRequest{Image: suite.Image})

	sc := pod.Spec.SecurityContext
	require.NotNil(t, sc, "pod SecurityContext must not be nil")

	require.NotNil(t, sc.RunAsNonRoot, "RunAsNonRoot must be set")
	assert.True(t, *sc.RunAsNonRoot, "RunAsNonRoot must be true")

	require.NotNil(t, sc.RunAsUser, "RunAsUser must be set")
	assert.Equal(t, int64(1000), *sc.RunAsUser, "RunAsUser must be 1000")

	require.NotNil(t, sc.RunAsGroup, "RunAsGroup must be set")
	assert.Equal(t, int64(1000), *sc.RunAsGroup, "RunAsGroup must be 1000")

	require.NotNil(t, sc.FSGroup, "FSGroup must be set")
	assert.Equal(t, int64(1000), *sc.FSGroup, "FSGroup must be 1000")

	require.NotNil(t, sc.FSGroupChangePolicy, "FSGroupChangePolicy must be set")
	assert.Equal(t, v1.FSGroupChangeOnRootMismatch, *sc.FSGroupChangePolicy,
		"FSGroupChangePolicy must be OnRootMismatch")
}

// ---------------------------------------------------------------------------
// Container-level SecurityContext (K8s "restricted" PSS compliance)
// ---------------------------------------------------------------------------

// TestPodSpec_ContainerSecurityContext verifies the container's security context
// satisfies the Kubernetes "restricted" Pod Security Standard:
//   - allowPrivilegeEscalation: false
//   - capabilities.drop: ["ALL"]
//   - seccompProfile.type: RuntimeDefault
func TestPodSpec_ContainerSecurityContext(t *testing.T) {
	_, pod := setupPod(t, "spec-ctn-sc", CreateRequest{Image: suite.Image})

	require.Len(t, pod.Spec.Containers, 1, "must have exactly one container")
	csc := pod.Spec.Containers[0].SecurityContext
	require.NotNil(t, csc, "container SecurityContext must not be nil")

	require.NotNil(t, csc.AllowPrivilegeEscalation, "AllowPrivilegeEscalation must be set")
	assert.False(t, *csc.AllowPrivilegeEscalation,
		"AllowPrivilegeEscalation must be false (PSS restricted)")

	require.NotNil(t, csc.Capabilities, "Capabilities must be set")
	require.NotEmpty(t, csc.Capabilities.Drop, "Capabilities.Drop must not be empty")
	assert.Contains(t, csc.Capabilities.Drop, v1.Capability("ALL"),
		"Capabilities.Drop must include ALL (PSS restricted)")

	require.NotNil(t, csc.SeccompProfile, "SeccompProfile must be set")
	assert.Equal(t, v1.SeccompProfileTypeRuntimeDefault, csc.SeccompProfile.Type,
		"SeccompProfile.Type must be RuntimeDefault (PSS restricted)")
}

// TestPodSpec_WorkspaceInitContainerSecurityContext verifies the workspace
// init container stays inside the restricted Pod Security Standard and the
// same non-root UID/GID contract as managed workload containers.
func TestPodSpec_WorkspaceInitContainerSecurityContext(t *testing.T) {
	_, pod := setupPod(t, "spec-init-sc", CreateRequest{Image: suite.Image})

	require.Len(t, pod.Spec.InitContainers, 1, "writable workspace mounts must have exactly one init container")
	isc := pod.Spec.InitContainers[0].SecurityContext
	require.NotNil(t, isc, "init container SecurityContext must not be nil")

	require.NotNil(t, isc.RunAsNonRoot, "init RunAsNonRoot must be set")
	assert.True(t, *isc.RunAsNonRoot, "init RunAsNonRoot must be true")

	require.NotNil(t, isc.RunAsUser, "init RunAsUser must be set")
	assert.Equal(t, int64(1000), *isc.RunAsUser, "init RunAsUser must be 1000")

	require.NotNil(t, isc.RunAsGroup, "init RunAsGroup must be set")
	assert.Equal(t, int64(1000), *isc.RunAsGroup, "init RunAsGroup must be 1000")

	require.NotNil(t, isc.AllowPrivilegeEscalation, "init AllowPrivilegeEscalation must be set")
	assert.False(t, *isc.AllowPrivilegeEscalation,
		"init AllowPrivilegeEscalation must be false (PSS restricted)")

	require.NotNil(t, isc.Capabilities, "init Capabilities must be set")
	require.NotEmpty(t, isc.Capabilities.Drop, "init Capabilities.Drop must not be empty")
	assert.Contains(t, isc.Capabilities.Drop, v1.Capability("ALL"),
		"init Capabilities.Drop must include ALL (PSS restricted)")

	require.NotNil(t, isc.SeccompProfile, "init SeccompProfile must be set")
	assert.Equal(t, v1.SeccompProfileTypeRuntimeDefault, isc.SeccompProfile.Type,
		"init SeccompProfile.Type must be RuntimeDefault (PSS restricted)")
}

// ---------------------------------------------------------------------------
// Volume and workspace mount
// ---------------------------------------------------------------------------

// TestPodSpec_WorkspaceVolumeMount verifies the workspace PVC is mounted correctly:
//   - volume name "workspace" backed by the expected binding PVC
//   - binding PVC is mounted at the AFSCP plan mount path
func TestPodSpec_WorkspaceVolumeMount(t *testing.T) {
	const wsID = "ws-spec-vol"
	wlID := uniqueID("spec-vol")
	req := CreateRequest{Image: suite.Image}
	ensureWorkspaceBindingForWorkload(t, wsID, testProj, wlID, &req)

	podName := workloadPodName(wsID, testProj, wlID)
	createCtx, cancelCreate := context.WithCancel(context.Background())
	defer cancelCreate()
	createDone := startCreateWorkloadRequest(createCtx, wsID, testProj, wlID, req)
	createDoneDrained := false
	defer func() {
		cancelCreate()
		if createDoneDrained {
			return
		}
		select {
		case err := <-createDone:
			require.NoError(t, err)
		case <-time.After(15 * time.Second):
			t.Error("create workload request did not finish after cancellation")
		}
	}()
	t.Cleanup(func() {
		cancelCreate()
		newClient().DeleteWorkload(t, wsID, testProj, wlID)
	})

	waitPodExists(t, suite.Namespace, podName, 10*time.Second)
	pod := fetchPod(t, podName)

	// Volume definition.
	require.Len(t, pod.Spec.Volumes, 1, "must have exactly one volume")
	vol := pod.Spec.Volumes[0]
	assert.Equal(t, "workspace", vol.Name)
	require.NotNil(t, vol.PersistentVolumeClaim, "volume must be backed by a PVC")
	pvcName := workspacebinding.PVCName(wsID, testProj, req.WorkspaceBindingID)
	assert.Equal(t, pvcName,
		vol.PersistentVolumeClaim.ClaimName,
		"PVC claim name must match the expected binding PVC")

	// Volume mount.
	require.Len(t, pod.Spec.Containers, 1)
	vms := pod.Spec.Containers[0].VolumeMounts
	require.Len(t, vms, 1, "must have exactly one volume mount")
	vm := vms[0]
	assert.Equal(t, "workspace", vm.Name)
	assert.Equal(t, taskHomePath(wlID), vm.MountPath, "workspace PVC must be mounted at the AFSCP plan mount path")
	assert.Empty(t, vm.SubPath, "payload subdir is carried by the PV mountOptions plan")

	payloadSubdir := pod.Annotations["mbos.io/payload-volume-subdir"]
	require.NotEmpty(t, payloadSubdir, "pod annotations must retain the AFSCP payload path for audit")
	pvc, err := k8sCli.CoreV1().PersistentVolumeClaims(suite.Namespace).Get(context.Background(), pvcName, metav1.GetOptions{})
	require.NoError(t, err, "workspace PVC must exist")
	require.NotEmpty(t, pvc.Spec.VolumeName, "workspace PVC must bind to a PV")
	pv, err := k8sCli.CoreV1().PersistentVolumes().Get(context.Background(), pvc.Spec.VolumeName, metav1.GetOptions{})
	require.NoError(t, err, "workspace PV must exist")
	assert.Contains(t, pv.Spec.MountOptions, "subdir="+payloadSubdir,
		"PV mountOptions must carry the AFSCP payload path used by JuiceFS CSI")
	if pv.Spec.CSI != nil {
		assert.Empty(t, pv.Spec.CSI.VolumeAttributes["subdir"],
			"VolumeAttributes[subdir] must not be the isolation source")
	}

	deleteResp := newClient().DeleteWorkload(t, wsID, testProj, wlID)
	require.True(t, isConfirmedWorkloadDeleteStatus(deleteResp.StatusCode),
		"delete inspected workload: expected confirmed release status, got %d: %s",
		deleteResp.StatusCode, deleteResp.BodyString())
	select {
	case err := <-createDone:
		createDoneDrained = true
		require.NoError(t, err)
	case <-time.After(15 * time.Second):
		t.Fatal("create workload request did not finish after deleting inspected pod")
	}
}

// ---------------------------------------------------------------------------
// Container configuration
// ---------------------------------------------------------------------------

// TestPodSpec_WorkingDirIsWorkspace verifies the container's workingDir is task HOME/workspace.
func TestPodSpec_WorkingDirIsWorkspace(t *testing.T) {
	wlID, pod := setupPod(t, "spec-cwd", CreateRequest{Image: suite.Image})

	require.Len(t, pod.Spec.Containers, 1)
	assert.Equal(t, taskWorkspacePath(wlID), pod.Spec.Containers[0].WorkingDir,
		"container workingDir must be task HOME/workspace")
}

// TestPodSpec_RuntimeEnvVarsInjected verifies TASK_HOME, HOME, and WORKSPACE_PATH
// are present in the container's environment, even when the caller sends no env block.
func TestPodSpec_RuntimeEnvVarsInjected(t *testing.T) {
	wlID, pod := setupPod(t, "spec-env-ws", CreateRequest{Image: suite.Image})

	require.Len(t, pod.Spec.Containers, 1)
	envMap := make(map[string]string)
	for _, e := range pod.Spec.Containers[0].Env {
		envMap[e.Name] = e.Value
	}
	assert.Equal(t, taskHomePath(wlID), envMap["TASK_HOME"],
		"TASK_HOME must always be injected into the container environment")
	assert.Equal(t, taskHomePath(wlID), envMap["HOME"],
		"HOME must match TASK_HOME")
	assert.Equal(t, taskWorkspacePath(wlID), envMap["WORKSPACE_PATH"],
		"WORKSPACE_PATH must match the plan-derived workspace path")
}

// TestPodSpec_UserEnvVarsInjected verifies caller-supplied env vars are forwarded
// to the container alongside the mandatory runtime path env vars.
func TestPodSpec_UserEnvVarsInjected(t *testing.T) {
	wlID, pod := setupPod(t, "spec-env-user", CreateRequest{
		Image: suite.Image,
		Env: map[string]string{
			"MY_SECRET":   "abc123",
			"MY_ENDPOINT": "https://example.com",
		},
	})

	require.Len(t, pod.Spec.Containers, 1)
	envMap := make(map[string]string)
	for _, e := range pod.Spec.Containers[0].Env {
		envMap[e.Name] = e.Value
	}
	assert.Equal(t, "abc123", envMap["MY_SECRET"], "MY_SECRET must be forwarded to container")
	assert.Equal(t, "https://example.com", envMap["MY_ENDPOINT"], "MY_ENDPOINT must be forwarded")
	assert.Equal(t, taskHomePath(wlID), envMap["TASK_HOME"], "TASK_HOME must still be present")
	assert.Equal(t, taskHomePath(wlID), envMap["HOME"], "HOME must still be present")
	assert.Equal(t, taskWorkspacePath(wlID), envMap["WORKSPACE_PATH"], "WORKSPACE_PATH must still be present")
}

// TestPodSpec_DefaultCommandIsKeepAlive verifies that when no command is specified
// the container uses the default keep-alive command [tail, -f, /dev/null].
func TestPodSpec_DefaultCommandIsKeepAlive(t *testing.T) {
	_, pod := setupPod(t, "spec-cmd-default", CreateRequest{Image: suite.Image})

	require.Len(t, pod.Spec.Containers, 1)
	assert.Equal(t, []string{"tail", "-f", "/dev/null"},
		pod.Spec.Containers[0].Command,
		"default command must be [tail -f /dev/null]")
}

// TestPodSpec_CustomCommandApplied verifies that a caller-specified command is
// forwarded verbatim to the container spec.
func TestPodSpec_CustomCommandApplied(t *testing.T) {
	customCmd := []string{"python3", "-m", "http.server", "8080"}
	_, pod := setupPod(t, "spec-cmd-custom", CreateRequest{
		Image:   suite.Image,
		Command: customCmd,
	})

	require.Len(t, pod.Spec.Containers, 1)
	assert.Equal(t, customCmd, pod.Spec.Containers[0].Command,
		"custom command must be forwarded to the container")
}

// ---------------------------------------------------------------------------
// Restart policy and service account
// ---------------------------------------------------------------------------

// TestPodSpec_RestartPolicyNever verifies RestartPolicy=Never so that the workload
// Pod never restarts automatically after an agent process exits.
func TestPodSpec_RestartPolicyNever(t *testing.T) {
	_, pod := setupPod(t, "spec-restart", CreateRequest{Image: suite.Image})

	assert.Equal(t, v1.RestartPolicyNever, pod.Spec.RestartPolicy,
		"RestartPolicy must be Never (agents must not auto-restart)")
}

// TestPodSpec_AutomountSATokenFalse verifies the pod does not automatically
// mount the Kubernetes service account token (reduces attack surface).
func TestPodSpec_AutomountSATokenFalse(t *testing.T) {
	_, pod := setupPod(t, "spec-sa", CreateRequest{Image: suite.Image})

	require.NotNil(t, pod.Spec.AutomountServiceAccountToken,
		"AutomountServiceAccountToken must be explicitly set (not left at default)")
	assert.False(t, *pod.Spec.AutomountServiceAccountToken,
		"AutomountServiceAccountToken must be false (least privilege)")
}

// TestPodSpec_TerminationGracePeriod verifies the termination grace period
// is set to 30 seconds.
func TestPodSpec_TerminationGracePeriod(t *testing.T) {
	_, pod := setupPod(t, "spec-term", CreateRequest{Image: suite.Image})

	require.NotNil(t, pod.Spec.TerminationGracePeriodSeconds,
		"TerminationGracePeriodSeconds must be explicitly set")
	assert.Equal(t, int64(30), *pod.Spec.TerminationGracePeriodSeconds,
		"TerminationGracePeriodSeconds must be 30")
}

// ---------------------------------------------------------------------------
// Resource requests and limits
// ---------------------------------------------------------------------------

// TestPodSpec_ResourceRequestsApplied verifies CPU and memory requests from the
// CreateRequest are reflected in the pod container spec.
func TestPodSpec_ResourceRequestsApplied(t *testing.T) {
	_, pod := setupPod(t, "spec-req", CreateRequest{
		Image:         suite.Image,
		CPURequest:    "100m",
		MemoryRequest: "128Mi",
	})

	require.Len(t, pod.Spec.Containers, 1)
	res := pod.Spec.Containers[0].Resources

	require.NotNil(t, res.Requests, "requests must be set")
	cpu := res.Requests[v1.ResourceCPU]
	mem := res.Requests[v1.ResourceMemory]
	assert.True(t, cpu.Equal(resource.MustParse("100m")),
		"cpu_request 100m must appear in pod spec, got %s", cpu.String())
	assert.True(t, mem.Equal(resource.MustParse("128Mi")),
		"memory_request 128Mi must appear in pod spec, got %s", mem.String())
}

// TestPodSpec_ResourceLimitsApplied verifies CPU and memory limits are reflected
// in the pod container spec.
func TestPodSpec_ResourceLimitsApplied(t *testing.T) {
	_, pod := setupPod(t, "spec-lim", CreateRequest{
		Image:       suite.Image,
		CPULimit:    "500m",
		MemoryLimit: "512Mi",
	})

	require.Len(t, pod.Spec.Containers, 1)
	res := pod.Spec.Containers[0].Resources

	require.NotNil(t, res.Limits, "limits must be set")
	cpu := res.Limits[v1.ResourceCPU]
	mem := res.Limits[v1.ResourceMemory]
	assert.True(t, cpu.Equal(resource.MustParse("500m")),
		"cpu_limit 500m must appear in pod spec, got %s", cpu.String())
	assert.True(t, mem.Equal(resource.MustParse("512Mi")),
		"memory_limit 512Mi must appear in pod spec, got %s", mem.String())
}

// TestPodSpec_ResourceRequestsAndLimits verifies all four resource fields are
// applied together when all are specified in a single request.
func TestPodSpec_ResourceRequestsAndLimits(t *testing.T) {
	_, pod := setupPod(t, "spec-res-all", CreateRequest{
		Image:         suite.Image,
		CPURequest:    "100m",
		CPULimit:      "500m",
		MemoryRequest: "128Mi",
		MemoryLimit:   "512Mi",
	})

	require.Len(t, pod.Spec.Containers, 1)
	res := pod.Spec.Containers[0].Resources

	require.NotNil(t, res.Requests)
	require.NotNil(t, res.Limits)
	assert.True(t, res.Requests[v1.ResourceCPU].Equal(resource.MustParse("100m")))
	assert.True(t, res.Limits[v1.ResourceCPU].Equal(resource.MustParse("500m")))
	assert.True(t, res.Requests[v1.ResourceMemory].Equal(resource.MustParse("128Mi")))
	assert.True(t, res.Limits[v1.ResourceMemory].Equal(resource.MustParse("512Mi")))
}

// TestPodSpec_NoResourcesWhenNotRequested verifies that when the CreateRequest omits
// all resource fields, the pod spec has no Requests or Limits (K8s defaults apply).
func TestPodSpec_NoResourcesWhenNotRequested(t *testing.T) {
	_, pod := setupPod(t, "spec-no-res", CreateRequest{Image: suite.Image})

	require.Len(t, pod.Spec.Containers, 1)
	res := pod.Spec.Containers[0].Resources
	assert.Nil(t, res.Requests, "Requests must be nil when no cpu_request/memory_request given")
	assert.Nil(t, res.Limits, "Limits must be nil when no cpu_limit/memory_limit given")
}

// ---------------------------------------------------------------------------
// Full pod lifecycle: Pending → Running (integration smoke)
// ---------------------------------------------------------------------------

// TestPodSpec_PodBecomesRunning verifies that a pod created by ASBCP
// eventually transitions to the Running phase, indicating the image was pulled
// and the container started successfully.
func TestPodSpec_PodBecomesRunning(t *testing.T) {
	wlID := uniqueID("spec-running")
	_ = mustCreateWorkload(t, testWS, testProj, wlID, CreateRequest{Image: suite.Image})
	t.Cleanup(func() { newClient().DeleteWorkload(t, testWS, testProj, wlID) })

	status := waitWorkloadRunning(t, testWS, testProj, wlID, 5*time.Minute)
	assert.Equal(t, "Running", status.Phase)
	assert.NotEmpty(t, status.PodName)
	assert.NotEmpty(t, status.IP, "running pod must have an IP address")
}
