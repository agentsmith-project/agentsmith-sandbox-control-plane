package k8s

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"log"
	"strconv"
	"time"

	v1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// PodSpec contains specifications for creating a sandbox pod
type PodSpec struct {
	SessionID             string
	Image                 string
	ImagePullPolicy       string
	ImagePullSecrets      []string
	TTLSeconds            int
	CPULimit              string
	MemoryLimit           string
	EphemeralStorageLimit string
	ContainerName         string
	Workdir               string
	Env                   map[string]string
	ResourceRequests      ResourceRequests
	ResourceLimits        ResourceLimits
	Labels                map[string]string
	Annotations           map[string]string
	Volumes               []VolumeSpec
	SecurityContext       *PodSecurityConfig
}

// ResourceRequests contains resource request values
type ResourceRequests struct {
	CPU    string
	Memory string
}

// ResourceLimits contains resource limit values
type ResourceLimits struct {
	CPU              string
	Memory           string
	EphemeralStorage string
}

// VolumeSpec contains volume specification
type VolumeSpec struct {
	Name      string
	MountPath string
	SizeLimit string
}

// PodSecurityConfig contains security context settings
type PodSecurityConfig struct {
	NonRoot             bool
	RunAsUser           int64
	DropAllCapabilities bool
	ReadOnlyRoot        bool
}

// PodResult contains the result of a pod operation
type PodResult struct {
	PodName   string
	ExpiresAt string
	Exists    bool
	Ready     bool
}

// CreatePod creates a new sandbox pod
func (c *Client) CreatePod(ctx context.Context, spec *PodSpec) (*PodResult, error) {
	podName := PodName(spec.SessionID)
	now := time.Now().UTC()
	expiresAt := now.Add(time.Duration(spec.TTLSeconds) * time.Second)

	pod := &v1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:      podName,
			Namespace: c.namespace,
			Labels:    spec.Labels,
			Annotations: mergeAnnotations(spec.Annotations, map[string]string{
				"sandbox/sessionId":    spec.SessionID,
				"sandbox/ttlSeconds":   strconv.Itoa(spec.TTLSeconds),
				"sandbox/lastActiveAt": now.Format(time.RFC3339),
				"sandbox/expiresAt":    expiresAt.Format(time.RFC3339),
			}),
		},
		Spec: buildPodSpec(spec),
	}

	createdPod, err := c.clientset.CoreV1().Pods(c.namespace).Create(ctx, pod, metav1.CreateOptions{})
	if err != nil {
		return nil, fmt.Errorf("failed to create pod: %w", err)
	}

	log.Printf("K8s: created pod %s (session=%s, expiresAt=%s)", podName, spec.SessionID, expiresAt.Format(time.RFC3339))

	return &PodResult{
		PodName:   createdPod.Name,
		ExpiresAt: expiresAt.Format(time.RFC3339),
		Exists:    true,
		Ready:     false,
	}, nil
}

// GetPod retrieves a pod by name
func (c *Client) GetPod(ctx context.Context, name string) (*v1.Pod, error) {
	pod, err := c.clientset.CoreV1().Pods(c.namespace).Get(ctx, name, metav1.GetOptions{})
	if err != nil {
		return nil, fmt.Errorf("failed to get pod: %w", err)
	}
	return pod, nil
}

// GetPodBySessionID retrieves a pod by session ID
func (c *Client) GetPodBySessionID(ctx context.Context, sessionID string) (*v1.Pod, error) {
	podName := PodName(sessionID)
	return c.GetPod(ctx, podName)
}

// PodExists checks if a pod exists
func (c *Client) PodExists(ctx context.Context, name string) (bool, error) {
	_, err := c.clientset.CoreV1().Pods(c.namespace).Get(ctx, name, metav1.GetOptions{})
	if err != nil {
		if errors.IsNotFound(err) {
			return false, nil
		}
		return false, err
	}
	return true, nil
}

// IsPodReady checks if a pod is ready
func IsPodReady(pod *v1.Pod) bool {
	for _, condition := range pod.Status.Conditions {
		if condition.Type == v1.PodReady && condition.Status == v1.ConditionTrue {
			return true
		}
	}
	return false
}

// WaitForPodReady waits for a pod to become ready
func (c *Client) WaitForPodReady(ctx context.Context, name string, waitTime time.Duration, pollInterval time.Duration) (bool, error) {
	timeout := time.After(waitTime)
	ticker := time.NewTicker(pollInterval)
	defer ticker.Stop()

	for {
		select {
		case <-timeout:
			return false, fmt.Errorf("pod %s did not become ready within %v", name, waitTime)
		case <-ctx.Done():
			return false, ctx.Err()
		case <-ticker.C:
			pod, err := c.clientset.CoreV1().Pods(c.namespace).Get(ctx, name, metav1.GetOptions{})
			if err != nil {
				if errors.IsNotFound(err) {
					return false, fmt.Errorf("pod %s not found", name)
				}
				return false, fmt.Errorf("failed to get pod: %w", err)
			}

			if pod.Status.Phase == v1.PodFailed {
				return false, fmt.Errorf("pod %s failed", name)
			}

			if IsPodReady(pod) {
				log.Printf("K8s: pod %s is ready", name)
				return true, nil
			}
		}
	}
}

// EnsurePod gets or creates a pod, waiting for it to be ready
func (c *Client) EnsurePod(ctx context.Context, spec *PodSpec, waitTime time.Duration, pollInterval time.Duration) (*PodResult, error) {
	podName := PodName(spec.SessionID)

	// Try to get existing pod
	pod, err := c.clientset.CoreV1().Pods(c.namespace).Get(ctx, podName, metav1.GetOptions{})
	if err == nil {
		// Pod exists
		if IsPodReady(pod) {
			return &PodResult{
				PodName: podName,
				Exists:  true,
				Ready:   true,
			}, nil
		}

		// Wait for ready
		ready, err := c.WaitForPodReady(ctx, podName, waitTime, pollInterval)
		if err != nil {
			return nil, err
		}
		return &PodResult{
			PodName: podName,
			Exists:  true,
			Ready:   ready,
		}, nil
	}

	if !errors.IsNotFound(err) {
		return nil, fmt.Errorf("failed to get pod: %w", err)
	}

	// Create new pod
	result, err := c.CreatePod(ctx, spec)
	if err != nil {
		// Handle race condition: another goroutine might have created it
		if errors.IsAlreadyExists(err) {
			// Wait for ready
			ready, err := c.WaitForPodReady(ctx, podName, waitTime, pollInterval)
			if err != nil {
				return nil, err
			}
			return &PodResult{
				PodName: podName,
				Exists:  true,
				Ready:   ready,
			}, nil
		}
		return nil, err
	}

	// Wait for the new pod to be ready
	ready, err := c.WaitForPodReady(ctx, podName, waitTime, pollInterval)
	if err != nil {
		return result, err
	}
	result.Ready = ready

	return result, nil
}

// DeletePod deletes a pod
func (c *Client) DeletePod(ctx context.Context, name string, gracePeriodSeconds int64) error {
	deleteOpts := metav1.DeleteOptions{}
	if gracePeriodSeconds >= 0 {
		deleteOpts.GracePeriodSeconds = &gracePeriodSeconds
	}

	err := c.clientset.CoreV1().Pods(c.namespace).Delete(ctx, name, deleteOpts)
	if err != nil && !errors.IsNotFound(err) {
		return fmt.Errorf("failed to delete pod: %w", err)
	}

	log.Printf("K8s: deleted pod %s", name)
	return nil
}

// DeletePodBySessionID deletes a pod by session ID
func (c *Client) DeletePodBySessionID(ctx context.Context, sessionID string, gracePeriodSeconds int64) error {
	podName := PodName(sessionID)
	return c.DeletePod(ctx, podName, gracePeriodSeconds)
}

// PatchActivity updates the activity timestamp and expiry of a pod
func (c *Client) PatchActivity(ctx context.Context, name string, ttlSeconds int) error {
	now := time.Now().UTC()
	expiresAt := now.Add(time.Duration(ttlSeconds) * time.Second)

	patch := map[string]interface{}{
		"metadata": map[string]interface{}{
			"annotations": map[string]string{
				"sandbox/lastActiveAt": now.Format(time.RFC3339),
				"sandbox/expiresAt":    expiresAt.Format(time.RFC3339),
			},
		},
	}

	patchBytes, _ := json.Marshal(patch)
	_, err := c.clientset.CoreV1().Pods(c.namespace).Patch(ctx, name, "application/merge-patch+json", patchBytes, metav1.PatchOptions{})
	if err != nil {
		return fmt.Errorf("failed to patch pod: %w", err)
	}

	log.Printf("K8s: patched activity for pod %s (expiresAt=%s)", name, expiresAt.Format(time.RFC3339))
	return nil
}

// PatchActivityBySessionID updates the activity timestamp by session ID
func (c *Client) PatchActivityBySessionID(ctx context.Context, sessionID string, ttlSeconds int) error {
	podName := PodName(sessionID)
	return c.PatchActivity(ctx, podName, ttlSeconds)
}

// PodName generates the pod name from a session ID
func PodName(sessionID string) string {
	hash := sha256.Sum256([]byte(sessionID))
	return "sbx-" + hex.EncodeToString(hash[:])[:10]
}

// buildPodSpec builds the pod spec from a PodSpec
func buildPodSpec(spec *PodSpec) v1.PodSpec {
	podSpec := v1.PodSpec{
		AutomountServiceAccountToken:  func() *bool { b := false; return &b }(),
		RestartPolicy:                 v1.RestartPolicyNever,
		TerminationGracePeriodSeconds: func() *int64 { i := int64(1); return &i }(),
		SecurityContext: &v1.PodSecurityContext{
			SeccompProfile: &v1.SeccompProfile{
				Type: v1.SeccompProfileTypeRuntimeDefault,
			},
		},
		Containers: []v1.Container{buildContainer(spec)},
		Volumes:    buildVolumes(spec.Volumes),
	}

	if len(spec.ImagePullSecrets) > 0 {
		secrets := make([]v1.LocalObjectReference, 0, len(spec.ImagePullSecrets))
		for _, name := range spec.ImagePullSecrets {
			if name == "" {
				continue
			}
			secrets = append(secrets, v1.LocalObjectReference{Name: name})
		}
		if len(secrets) > 0 {
			podSpec.ImagePullSecrets = secrets
		}
	}

	return podSpec
}

// buildContainer builds the container spec from a PodSpec
func buildContainer(spec *PodSpec) v1.Container {
	container := v1.Container{
		Name:            spec.ContainerName,
		Image:           spec.Image,
		ImagePullPolicy: v1.PullPolicy(spec.ImagePullPolicy),
		Command:         []string{"sh", "-lc", "tail -f /dev/null"},
		WorkingDir:      spec.Workdir,
		VolumeMounts:    buildVolumeMounts(spec.Volumes),
		Resources:       buildResources(spec.ResourceRequests, spec.ResourceLimits),
	}

	// Add environment variables
	if len(spec.Env) > 0 {
		env := make([]v1.EnvVar, 0, len(spec.Env))
		for k, v := range spec.Env {
			env = append(env, v1.EnvVar{
				Name:  k,
				Value: v,
			})
		}
		container.Env = env
	}

	if spec.SecurityContext != nil {
		container.SecurityContext = buildSecurityContext(spec.SecurityContext)
	}

	return container
}

// buildSecurityContext builds the security context
func buildSecurityContext(cfg *PodSecurityConfig) *v1.SecurityContext {
	ctx := &v1.SecurityContext{
		RunAsNonRoot:             &cfg.NonRoot,
		RunAsUser:                &cfg.RunAsUser,
		AllowPrivilegeEscalation: func() *bool { b := false; return &b }(),
		ReadOnlyRootFilesystem:   &cfg.ReadOnlyRoot,
	}

	if cfg.DropAllCapabilities {
		ctx.Capabilities = &v1.Capabilities{
			Drop: []v1.Capability{"ALL"},
		}
	}

	return ctx
}

// buildResources builds resource requirements
func buildResources(requests ResourceRequests, limits ResourceLimits) v1.ResourceRequirements {
	resources := v1.ResourceRequirements{
		Requests: v1.ResourceList{},
		Limits:   v1.ResourceList{},
	}

	if requests.CPU != "" {
		resources.Requests[v1.ResourceCPU] = resource.MustParse(requests.CPU)
	}
	if requests.Memory != "" {
		resources.Requests[v1.ResourceMemory] = resource.MustParse(requests.Memory)
	}
	if limits.CPU != "" {
		resources.Limits[v1.ResourceCPU] = resource.MustParse(limits.CPU)
	}
	if limits.Memory != "" {
		resources.Limits[v1.ResourceMemory] = resource.MustParse(limits.Memory)
	}
	if limits.EphemeralStorage != "" {
		resources.Limits[v1.ResourceEphemeralStorage] = resource.MustParse(limits.EphemeralStorage)
	}

	return resources
}

// buildVolumes builds volume specs
func buildVolumes(volumes []VolumeSpec) []v1.Volume {
	vols := make([]v1.Volume, 0, len(volumes))
	for _, v := range volumes {
		var sizeLimit *resource.Quantity
		if v.SizeLimit != "" && v.SizeLimit != "0" {
			qty := resource.MustParse(v.SizeLimit)
			sizeLimit = &qty
		}

		vols = append(vols, v1.Volume{
			Name: v.Name,
			VolumeSource: v1.VolumeSource{
				EmptyDir: &v1.EmptyDirVolumeSource{
					SizeLimit: sizeLimit,
				},
			},
		})
	}
	return vols
}

// buildVolumeMounts builds volume mount specs
func buildVolumeMounts(volumes []VolumeSpec) []v1.VolumeMount {
	mounts := make([]v1.VolumeMount, 0, len(volumes))
	for _, v := range volumes {
		mounts = append(mounts, v1.VolumeMount{
			Name:      v.Name,
			MountPath: v.MountPath,
		})
	}
	return mounts
}

// mergeAnnotations merges multiple annotation maps
func mergeAnnotations(annotations ...map[string]string) map[string]string {
	result := make(map[string]string)
	for _, a := range annotations {
		for k, v := range a {
			result[k] = v
		}
	}
	return result
}

// GetTTLFromPod extracts TTL from pod annotations
func GetTTLFromPod(pod *v1.Pod) int {
	if pod.Annotations == nil {
		return 0
	}
	ttlStr := pod.Annotations["sandbox/ttlSeconds"]
	ttl, _ := strconv.Atoi(ttlStr)
	return ttl
}

// GetSessionIDFromPod extracts session ID from pod annotations
func GetSessionIDFromPod(pod *v1.Pod) string {
	if pod.Annotations == nil {
		return ""
	}
	return pod.Annotations["sandbox/sessionId"]
}

// GetExpiresAtFromPod extracts expires at from pod annotations
func GetExpiresAtFromPod(pod *v1.Pod) string {
	if pod.Annotations == nil {
		return ""
	}
	return pod.Annotations["sandbox/expiresAt"]
}
