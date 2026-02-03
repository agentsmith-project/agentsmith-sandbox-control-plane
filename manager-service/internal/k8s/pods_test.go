package k8s

import (
	"testing"

	v1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func TestPodName(t *testing.T) {
	tests := []struct {
		name      string
		sessionID string
		wantLen   int
		wantStart string
	}{
		{
			name:      "normal session ID",
			sessionID: "test-session-123",
			wantLen:   14,
			wantStart: "sbx-",
		},
		{
			name:      "empty session ID",
			sessionID: "",
			wantLen:   14,
			wantStart: "sbx-",
		},
		{
			name:      "unicode session ID",
			sessionID: "session-测试-123",
			wantLen:   14,
			wantStart: "sbx-",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := PodName(tt.sessionID)
			if len(got) != tt.wantLen {
				t.Errorf("PodName() length = %v, want %v", len(got), tt.wantLen)
			}
			if len(got) < len(tt.wantStart) || got[:len(tt.wantStart)] != tt.wantStart {
				t.Errorf("PodName() should start with %v, got %v", tt.wantStart, got)
			}
		})
	}
}

func TestIsPodReady(t *testing.T) {
	tests := []struct {
		name string
		pod  *v1.Pod
		want bool
	}{
		{
			name: "ready pod",
			pod: &v1.Pod{
				Status: v1.PodStatus{
					Conditions: []v1.PodCondition{
						{
							Type:   v1.PodReady,
							Status: v1.ConditionTrue,
						},
					},
				},
			},
			want: true,
		},
		{
			name: "not ready pod",
			pod: &v1.Pod{
				Status: v1.PodStatus{
					Conditions: []v1.PodCondition{
						{
							Type:   v1.PodReady,
							Status: v1.ConditionFalse,
						},
					},
				},
			},
			want: false,
		},
		{
			name: "pod without ready condition",
			pod: &v1.Pod{
				Status: v1.PodStatus{
					Conditions: []v1.PodCondition{
						{
							Type:   v1.PodInitialized,
							Status: v1.ConditionTrue,
						},
					},
				},
			},
			want: false,
		},
		{
			name: "pod with no conditions",
			pod: &v1.Pod{
				Status: v1.PodStatus{
					Conditions: []v1.PodCondition{},
				},
			},
			want: false,
		},
		{
			name: "nil pod status conditions",
			pod: &v1.Pod{
				Status: v1.PodStatus{
					Conditions: nil,
				},
			},
			want: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := IsPodReady(tt.pod)
			if got != tt.want {
				t.Errorf("IsPodReady() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestGetTTLFromPod(t *testing.T) {
	tests := []struct {
		name string
		pod  *v1.Pod
		want int
	}{
		{
			name: "pod with TTL annotation",
			pod: &v1.Pod{
				ObjectMeta: metav1.ObjectMeta{
					Annotations: map[string]string{
						"sandbox/ttlSeconds": "900",
					},
				},
			},
			want: 900,
		},
		{
			name: "pod with TTL annotation zero",
			pod: &v1.Pod{
				ObjectMeta: metav1.ObjectMeta{
					Annotations: map[string]string{
						"sandbox/ttlSeconds": "0",
					},
				},
			},
			want: 0,
		},
		{
			name: "pod without TTL annotation",
			pod: &v1.Pod{
				ObjectMeta: metav1.ObjectMeta{
					Annotations: map[string]string{},
				},
			},
			want: 0,
		},
		{
			name: "pod with nil annotations",
			pod: &v1.Pod{
				ObjectMeta: metav1.ObjectMeta{
					Annotations: nil,
				},
			},
			want: 0,
		},
		{
			name: "pod with invalid TTL annotation",
			pod: &v1.Pod{
				ObjectMeta: metav1.ObjectMeta{
					Annotations: map[string]string{
						"sandbox/ttlSeconds": "invalid",
					},
				},
			},
			want: 0,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := GetTTLFromPod(tt.pod)
			if got != tt.want {
				t.Errorf("GetTTLFromPod() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestGetSessionIDFromPod(t *testing.T) {
	tests := []struct {
		name string
		pod  *v1.Pod
		want string
	}{
		{
			name: "pod with session ID annotation",
			pod: &v1.Pod{
				ObjectMeta: metav1.ObjectMeta{
					Annotations: map[string]string{
						"sandbox/sessionId": "test-session-123",
					},
				},
			},
			want: "test-session-123",
		},
		{
			name: "pod without session ID annotation",
			pod: &v1.Pod{
				ObjectMeta: metav1.ObjectMeta{
					Annotations: map[string]string{},
				},
			},
			want: "",
		},
		{
			name: "pod with nil annotations",
			pod: &v1.Pod{
				ObjectMeta: metav1.ObjectMeta{
					Annotations: nil,
				},
			},
			want: "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := GetSessionIDFromPod(tt.pod)
			if got != tt.want {
				t.Errorf("GetSessionIDFromPod() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestGetExpiresAtFromPod(t *testing.T) {
	tests := []struct {
		name string
		pod  *v1.Pod
		want string
	}{
		{
			name: "pod with expiresAt annotation",
			pod: &v1.Pod{
				ObjectMeta: metav1.ObjectMeta{
					Annotations: map[string]string{
						"sandbox/expiresAt": "2024-01-01T00:00:00Z",
					},
				},
			},
			want: "2024-01-01T00:00:00Z",
		},
		{
			name: "pod without expiresAt annotation",
			pod: &v1.Pod{
				ObjectMeta: metav1.ObjectMeta{
					Annotations: map[string]string{},
				},
			},
			want: "",
		},
		{
			name: "pod with nil annotations",
			pod: &v1.Pod{
				ObjectMeta: metav1.ObjectMeta{
					Annotations: nil,
				},
			},
			want: "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := GetExpiresAtFromPod(tt.pod)
			if got != tt.want {
				t.Errorf("GetExpiresAtFromPod() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestMergeAnnotations(t *testing.T) {
	tests := []struct {
		name        string
		annotations []map[string]string
		want        map[string]string
	}{
		{
			name: "merge two maps",
			annotations: []map[string]string{
				{"key1": "value1"},
				{"key2": "value2"},
			},
			want: map[string]string{
				"key1": "value1",
				"key2": "value2",
			},
		},
		{
			name: "merge three maps",
			annotations: []map[string]string{
				{"key1": "value1"},
				{"key2": "value2"},
				{"key3": "value3"},
			},
			want: map[string]string{
				"key1": "value1",
				"key2": "value2",
				"key3": "value3",
			},
		},
		{
			name: "merge with overlapping keys",
			annotations: []map[string]string{
				{"key1": "value1"},
				{"key1": "value2"},
			},
			want: map[string]string{
				"key1": "value2",
			},
		},
		{
			name:        "merge no maps",
			annotations: []map[string]string{},
			want:        map[string]string{},
		},
		{
			name: "merge empty maps",
			annotations: []map[string]string{
				{},
				{},
			},
			want: map[string]string{},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := mergeAnnotations(tt.annotations...)
			if len(got) != len(tt.want) {
				t.Errorf("mergeAnnotations() length = %v, want %v", len(got), len(tt.want))
			}
			for k, v := range tt.want {
				if got[k] != v {
					t.Errorf("mergeAnnotations()[%v] = %v, want %v", k, got[k], v)
				}
			}
		})
	}
}

func TestCreatePod(t *testing.T) {
	t.Skip("Skipping CreatePod test - requires real clientset")
}

func TestGetPod(t *testing.T) {
	t.Skip("Skipping GetPod test - requires real clientset")
}

func TestGetPodBySessionID(t *testing.T) {
	t.Skip("Skipping GetPodBySessionID test - requires real clientset")
}

func TestPodExists(t *testing.T) {
	t.Skip("Skipping PodExists test - requires real clientset")
}

func TestWaitForPodReady(t *testing.T) {
	t.Skip("Skipping WaitForPodReady test - requires real clientset")
}

func TestEnsurePod(t *testing.T) {
	t.Skip("Skipping EnsurePod test - requires real clientset")
}

func TestDeletePod(t *testing.T) {
	t.Skip("Skipping DeletePod test - requires real clientset")
}

func TestDeletePodBySessionID(t *testing.T) {
	t.Skip("Skipping DeletePodBySessionID test - requires real clientset")
}

func TestPatchActivity(t *testing.T) {
	t.Skip("Skipping PatchActivity test - requires real clientset")
}

func TestBuildPodSpec(t *testing.T) {
	spec := &PodSpec{
		SessionID:             "test-session",
		Image:                 "test-image:latest",
		ImagePullPolicy:       "IfNotPresent",
		TTLSeconds:            900,
		CPULimit:              "500m",
		MemoryLimit:           "512Mi",
		EphemeralStorageLimit: "1Gi",
		ContainerName:         "runner",
		Workdir:               "/workspace",
		Env:                   map[string]string{"KEY": "value"},
		ResourceRequests: ResourceRequests{
			CPU:    "100m",
			Memory: "128Mi",
		},
		ResourceLimits: ResourceLimits{
			CPU:              "500m",
			Memory:           "512Mi",
			EphemeralStorage: "1Gi",
		},
		Labels:      map[string]string{"app": "sandbox"},
		Annotations: map[string]string{"test": "value"},
		Volumes:     []VolumeSpec{},
		SecurityContext: &PodSecurityConfig{
			NonRoot:             true,
			RunAsUser:           10001,
			DropAllCapabilities: true,
			ReadOnlyRoot:        false,
		},
	}

	podSpec := buildPodSpec(spec)

	// Verify basic properties
	if podSpec.AutomountServiceAccountToken == nil || *podSpec.AutomountServiceAccountToken {
		t.Error("buildPodSpec() AutomountServiceAccountToken should be false")
	}

	if podSpec.RestartPolicy != v1.RestartPolicyNever {
		t.Errorf("buildPodSpec() RestartPolicy = %v, want %v", podSpec.RestartPolicy, v1.RestartPolicyNever)
	}

	if len(podSpec.Containers) != 1 {
		t.Errorf("buildPodSpec() containers count = %v, want 1", len(podSpec.Containers))
	}

	container := podSpec.Containers[0]
	if container.Name != spec.ContainerName {
		t.Errorf("buildPodSpec() container name = %v, want %v", container.Name, spec.ContainerName)
	}

	if container.Image != spec.Image {
		t.Errorf("buildPodSpec() container image = %v, want %v", container.Image, spec.Image)
	}

	if container.WorkingDir != spec.Workdir {
		t.Errorf("buildPodSpec() container workdir = %v, want %v", container.WorkingDir, spec.Workdir)
	}
}

func TestBuildResources(t *testing.T) {
	tests := []struct {
		name     string
		requests ResourceRequests
		limits   ResourceLimits
	}{
		{
			name: "full resources",
			requests: ResourceRequests{
				CPU:    "100m",
				Memory: "128Mi",
			},
			limits: ResourceLimits{
				CPU:              "500m",
				Memory:           "512Mi",
				EphemeralStorage: "1Gi",
			},
		},
		{
			name: "empty resources",
			requests: ResourceRequests{
				CPU:    "",
				Memory: "",
			},
			limits: ResourceLimits{
				CPU:              "",
				Memory:           "",
				EphemeralStorage: "",
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			resources := buildResources(tt.requests, tt.limits)

			// Just verify it doesn't panic
			if resources.Requests == nil {
				t.Error("buildResources() Requests is nil")
			}
			if resources.Limits == nil {
				t.Error("buildResources() Limits is nil")
			}
		})
	}
}

func TestBuildVolumes(t *testing.T) {
	volumes := []VolumeSpec{
		{
			Name:      "data",
			MountPath: "/data",
			SizeLimit: "1Gi",
		},
		{
			Name:      "cache",
			MountPath: "/cache",
			SizeLimit: "500Mi",
		},
	}

	builtVolumes := buildVolumes(volumes)

	if len(builtVolumes) != len(volumes) {
		t.Errorf("buildVolumes() count = %v, want %v", len(builtVolumes), len(volumes))
	}

	for i, v := range builtVolumes {
		if v.Name != volumes[i].Name {
			t.Errorf("buildVolumes()[%d].Name = %v, want %v", i, v.Name, volumes[i].Name)
		}
		if v.EmptyDir == nil {
			t.Errorf("buildVolumes()[%d].EmptyDir is nil", i)
		}
	}
}

func TestBuildVolumeMounts(t *testing.T) {
	volumes := []VolumeSpec{
		{
			Name:      "data",
			MountPath: "/data",
			SizeLimit: "1Gi",
		},
	}

	mounts := buildVolumeMounts(volumes)

	if len(mounts) != len(volumes) {
		t.Errorf("buildVolumeMounts() count = %v, want %v", len(mounts), len(volumes))
	}

	if mounts[0].Name != volumes[0].Name {
		t.Errorf("buildVolumeMounts()[0].Name = %v, want %v", mounts[0].Name, volumes[0].Name)
	}

	if mounts[0].MountPath != volumes[0].MountPath {
		t.Errorf("buildVolumeMounts()[0].MountPath = %v, want %v", mounts[0].MountPath, volumes[0].MountPath)
	}
}

func TestBuildSecurityContext(t *testing.T) {
	cfg := &PodSecurityConfig{
		NonRoot:             true,
		RunAsUser:           10001,
		DropAllCapabilities: true,
		ReadOnlyRoot:        false,
	}

	ctx := buildSecurityContext(cfg)

	if ctx.RunAsNonRoot == nil || *ctx.RunAsNonRoot != true {
		t.Error("buildSecurityContext() RunAsNonRoot not set correctly")
	}

	if ctx.RunAsUser == nil || *ctx.RunAsUser != 10001 {
		t.Error("buildSecurityContext() RunAsUser not set correctly")
	}

	if ctx.ReadOnlyRootFilesystem == nil || *ctx.ReadOnlyRootFilesystem != false {
		t.Error("buildSecurityContext() ReadOnlyRootFilesystem not set correctly")
	}

	if ctx.Capabilities == nil {
		t.Error("buildSecurityContext() Capabilities is nil")
	} else if len(ctx.Capabilities.Drop) == 0 {
		t.Error("buildSecurityContext() Capabilities.Drop is empty")
	}
}

// TestPodResult tests the PodResult struct
func TestPodResult(t *testing.T) {
	result := &PodResult{
		PodName:   "test-pod",
		ExpiresAt: "2024-01-01T00:15:00Z",
		Exists:    true,
		Ready:     true,
	}

	if result.PodName != "test-pod" {
		t.Errorf("PodName = %v, want 'test-pod'", result.PodName)
	}

	if !result.Exists {
		t.Error("Exists should be true")
	}

	if !result.Ready {
		t.Error("Ready should be true")
	}
}
