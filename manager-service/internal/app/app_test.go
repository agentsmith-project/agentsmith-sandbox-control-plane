package app

import "testing"

func TestIsWorkspaceBindingRouteUsesResourceSegment(t *testing.T) {
	tests := []struct {
		name string
		path string
		want bool
	}{
		{
			name: "workspace binding resource",
			path: "/v1/workspaces/ws-1/projects/proj-1/workspace-bindings/wmb-1",
			want: true,
		},
		{
			name: "workload id named workspace-bindings keepalive",
			path: "/v1/workspaces/ws-1/projects/proj-1/workloads/workspace-bindings/keepalive",
			want: false,
		},
		{
			name: "workload id named workspace-bindings exec",
			path: "/v1/workspaces/ws-1/projects/proj-1/workloads/workspace-bindings/exec",
			want: false,
		},
		{
			name: "workload id contains workspace-bindings",
			path: "/v1/workspaces/ws-1/projects/proj-1/workloads/my-workspace-bindings/exec",
			want: false,
		},
		{
			name: "malformed workspace path",
			path: "/v1/workspaces/ws-1/projects/proj-1",
			want: false,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := isWorkspaceBindingRoute(tt.path); got != tt.want {
				t.Fatalf("isWorkspaceBindingRoute(%q) = %v, want %v", tt.path, got, tt.want)
			}
		})
	}
}
