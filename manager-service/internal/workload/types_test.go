package workload_test

import (
	"encoding/json"
	"testing"

	"github.com/sandbox/manager/internal/workload"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestCreateRequest_JSON(t *testing.T) {
	raw := `{
		"image": "registry.example.com/myapp:latest",
		"command": ["python", "main.py"],
		"env": {"KEY1": "val1", "CUSTOM_URL": "ws://example:20000"},
		"workspace_binding_id": "wmb_demo",
		"idle_timeout_sec": 300,
		"max_lifetime_sec": 3600
	}`

	var req workload.CreateRequest
	err := json.Unmarshal([]byte(raw), &req)
	require.NoError(t, err)

	assert.Equal(t, "registry.example.com/myapp:latest", req.Image)
	assert.Equal(t, []string{"python", "main.py"}, req.Command)
	assert.Equal(t, "val1", req.Env["KEY1"])
	assert.Equal(t, "ws://example:20000", req.Env["CUSTOM_URL"])
	assert.Equal(t, "wmb_demo", req.WorkspaceBindingID)
	assert.Equal(t, 300, req.IdleTimeoutSec)
	assert.Equal(t, 3600, req.MaxLifetimeSec)
}

func TestCreateRequest_JSON_NoCommand(t *testing.T) {
	raw := `{"image": "ubuntu:22.04"}`

	var req workload.CreateRequest
	err := json.Unmarshal([]byte(raw), &req)
	require.NoError(t, err)

	assert.Equal(t, "ubuntu:22.04", req.Image)
	assert.Nil(t, req.Command)
}

func TestExecRequest_JSON(t *testing.T) {
	raw := `{"cmd": ["ls", "-la"], "timeout_seconds": 60}`

	var req workload.ExecRequest
	err := json.Unmarshal([]byte(raw), &req)
	require.NoError(t, err)

	assert.Equal(t, []string{"ls", "-la"}, req.Cmd)
	assert.Equal(t, 60, req.TimeoutSeconds)
}

func TestExecResponse_JSON(t *testing.T) {
	resp := workload.ExecResponse{
		ExitCode:   0,
		Stdout:     "hello world\n",
		Stderr:     "",
		DurationMs: 42,
	}

	data, err := json.Marshal(resp)
	require.NoError(t, err)

	var decoded workload.ExecResponse
	require.NoError(t, json.Unmarshal(data, &decoded))
	assert.Equal(t, resp, decoded)
}

func TestPodStatus_JSON_Roundtrip(t *testing.T) {
	status := workload.PodStatus{
		PodName:   "workload-xyz",
		Phase:     "Running",
		IP:        "10.0.0.1",
		StartedAt: "2026-03-01T00:00:00Z",
		ExpiresAt: "2026-03-01T01:00:00Z",
	}

	data, err := json.Marshal(status)
	require.NoError(t, err)

	var decoded workload.PodStatus
	require.NoError(t, json.Unmarshal(data, &decoded))

	assert.Equal(t, status, decoded)
}

func TestDeleteResponse_JSON(t *testing.T) {
	resp := workload.DeleteResponse{
		Message: "pod deleted",
	}

	data, err := json.Marshal(resp)
	require.NoError(t, err)
	assert.Contains(t, string(data), "pod deleted")
}

func TestKeepaliveResponse_JSON(t *testing.T) {
	resp := workload.KeepaliveResponse{
		ExpiresAt: "2026-03-01T01:00:00Z",
	}

	data, err := json.Marshal(resp)
	require.NoError(t, err)

	var decoded workload.KeepaliveResponse
	require.NoError(t, json.Unmarshal(data, &decoded))
	assert.Equal(t, resp, decoded)
}
