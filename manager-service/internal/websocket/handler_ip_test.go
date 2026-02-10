package websocket

import (
	"testing"
	v1 "k8s.io/api/core/v1"
)

func TestValidatePodIP(t *testing.T) {
	h := &Handler{}

	tests := []struct {
		name    string
		pod     *v1.Pod
		wantErr bool
	}{
		{
			name: "valid IP",
			pod: &v1.Pod{
				Status: v1.PodStatus{
					PodIP: "10.244.1.5",
				},
			},
			wantErr: false,
		},
		{
			name: "empty IP",
			pod: &v1.Pod{
				Status: v1.PodStatus{
					PodIP: "",
				},
			},
			wantErr: true,
		},
		{
			name: "invalid IP format",
			pod: &v1.Pod{
				Status: v1.PodStatus{
					PodIP: "not-an-ip",
				},
			},
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := h.validatePodIP(tt.pod)
			if (err != nil) != tt.wantErr {
				t.Errorf("validatePodIP() error = %v, wantErr %v", err, tt.wantErr)
			}
		})
	}
}
