package websocket

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestParseCreate_ValidPayload_ReturnsPayload tests parsing a valid create payload
func TestParseCreate_ValidPayload_ReturnsPayload(t *testing.T) {
	handler := &Handler{}

	payload := CreatePayload{
		AgentThreadID: "test-agent-123",
		Image:         "ubuntu:latest",
		Command:       []string{"bash", "-c", "echo hello"},
		Env:           map[string]string{"PATH": "/usr/bin"},
		Config: SecurityConfig{
			AllowNetworkAccess: true,
			CPULimit:           "500m",
		},
	}

	data, err := json.Marshal(payload)
	require.NoError(t, err)

	result, err := handler.parseCreate(data)
	assert.NoError(t, err)
	assert.Equal(t, "test-agent-123", result.AgentThreadID)
	assert.Equal(t, "ubuntu:latest", result.Image)
	assert.Equal(t, []string{"bash", "-c", "echo hello"}, result.Command)
	assert.Equal(t, map[string]string{"PATH": "/usr/bin"}, result.Env)
	assert.True(t, result.Config.AllowNetworkAccess)
	assert.Equal(t, "500m", result.Config.CPULimit)
}

// TestParseCreate_MissingAgentThreadID_ReturnsError tests parsing with missing agent_thread_id
func TestParseCreate_MissingAgentThreadID_ReturnsError(t *testing.T) {
	handler := &Handler{}

	payload := map[string]interface{}{
		"image": "ubuntu:latest",
	}

	data, err := json.Marshal(payload)
	require.NoError(t, err)

	result, err := handler.parseCreate(data)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "agent_thread_id is required")
	assert.Equal(t, CreatePayload{}, result)
}

// TestParseCreate_EmptyAgentThreadID_ReturnsError tests parsing with empty agent_thread_id
func TestParseCreate_EmptyAgentThreadID_ReturnsError(t *testing.T) {
	handler := &Handler{}

	payload := CreatePayload{
		AgentThreadID: "",
		Image:         "ubuntu:latest",
	}

	data, err := json.Marshal(payload)
	require.NoError(t, err)

	result, err := handler.parseCreate(data)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "agent_thread_id is required")
	assert.Equal(t, CreatePayload{}, result)
}

// TestParseCreate_InvalidJSON_ReturnsError tests parsing invalid JSON
func TestParseCreate_InvalidJSON_ReturnsError(t *testing.T) {
	handler := &Handler{}

	data := []byte("invalid json")

	result, err := handler.parseCreate(data)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "unmarshal failed")
	assert.Equal(t, CreatePayload{}, result)
}

// TestParseCreate_MinimalValidPayload_ReturnsPayload tests parsing minimal valid payload
func TestParseCreate_MinimalValidPayload_ReturnsPayload(t *testing.T) {
	handler := &Handler{}

	payload := map[string]interface{}{
		"agent_thread_id": "test-123",
	}

	data, err := json.Marshal(payload)
	require.NoError(t, err)

	result, err := handler.parseCreate(data)
	assert.NoError(t, err)
	assert.Equal(t, "test-123", result.AgentThreadID)
}

// TestParseCreate_WithAllFields_ReturnsPayload tests parsing with all fields populated
func TestParseCreate_WithAllFields_ReturnsPayload(t *testing.T) {
	handler := &Handler{}

	payload := CreatePayload{
		AgentThreadID: "test-agent-456",
		Image:         "python:3.11",
		Command:       []string{"python", "-u", "app.py"},
		Env: map[string]string{
			"PYTHONPATH": "/app",
			"DEBUG":      "false",
		},
		Config: SecurityConfig{
			AllowNetworkAccess:  false,
			ReadonlyFilesystem:  true,
			CPULimit:            "1000m",
			MemoryLimit:         "2Gi",
			IdleTimeout:         "30m",
			MaxLifetime:         "2h",
			DropAllCapabilities: true,
			AllowPrivileged:     false,
		},
	}

	data, err := json.Marshal(payload)
	require.NoError(t, err)

	result, err := handler.parseCreate(data)
	assert.NoError(t, err)
	assert.Equal(t, "test-agent-456", result.AgentThreadID)
	assert.Equal(t, "python:3.11", result.Image)
	assert.Equal(t, []string{"python", "-u", "app.py"}, result.Command)
	assert.Equal(t, map[string]string{"PYTHONPATH": "/app", "DEBUG": "false"}, result.Env)
	assert.False(t, result.Config.AllowNetworkAccess)
	assert.True(t, result.Config.ReadonlyFilesystem)
	assert.Equal(t, "1000m", result.Config.CPULimit)
	assert.Equal(t, "2Gi", result.Config.MemoryLimit)
	assert.Equal(t, "30m", result.Config.IdleTimeout)
	assert.Equal(t, "2h", result.Config.MaxLifetime)
	assert.True(t, result.Config.DropAllCapabilities)
	assert.False(t, result.Config.AllowPrivileged)
}

// TestParseStdin_ValidPayload_ReturnsPayload tests parsing a valid stdin payload
func TestParseStdin_ValidPayload_ReturnsPayload(t *testing.T) {
	handler := &Handler{}

	payload := StdinPayload{
		Data: "SGVsbG8gV29ybGQ=", // "Hello World" in base64
	}

	data, err := json.Marshal(payload)
	require.NoError(t, err)

	result, err := handler.parseStdin(data)
	assert.NoError(t, err)
	assert.Equal(t, "SGVsbG8gV29ybGQ=", result.Data)
}

// TestParseStdin_EmptyData_ReturnsPayload tests parsing with empty data
func TestParseStdin_EmptyData_ReturnsPayload(t *testing.T) {
	handler := &Handler{}

	payload := StdinPayload{
		Data: "",
	}

	data, err := json.Marshal(payload)
	require.NoError(t, err)

	result, err := handler.parseStdin(data)
	assert.NoError(t, err)
	assert.Equal(t, "", result.Data)
}

// TestParseStdin_InvalidJSON_ReturnsError tests parsing invalid JSON
func TestParseStdin_InvalidJSON_ReturnsError(t *testing.T) {
	handler := &Handler{}

	data := []byte("invalid json")

	result, err := handler.parseStdin(data)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "unmarshal failed")
	assert.Equal(t, StdinPayload{}, result)
}

// TestParseStdin_WithLargeBase64Data_ReturnsPayload tests parsing with large base64 data
func TestParseStdin_WithLargeBase64Data_ReturnsPayload(t *testing.T) {
	handler := &Handler{}

	// Create a larger base64 string
	largeData := "VGhpcyBpcyBhIGxvbmdlciBzdHJpbmcgd2l0aCBtb3JlIHRleHQgdG8gdGVzdCBwYXJzaW5n" +
		"IG9mIGxhcmdlciBzdGRpbiBtZXNzYWdlcy4="

	payload := StdinPayload{
		Data: largeData,
	}

	data, err := json.Marshal(payload)
	require.NoError(t, err)

	result, err := handler.parseStdin(data)
	assert.NoError(t, err)
	assert.Equal(t, largeData, result.Data)
}

// TestContainsMiddle tests the containsMiddle helper function
func TestContainsMiddle(t *testing.T) {
	tests := []struct {
		name     string
		s        string
		substr   string
		expected bool
	}{
		{
			name:     "substring at beginning",
			s:        "hello world",
			substr:   "hello",
			expected: true, // containsMiddle iterates through all positions
		},
		{
			name:     "substring at end",
			s:        "hello world",
			substr:   "world",
			expected: true, // containsMiddle iterates through all positions including end
		},
		{
			name:     "substring in middle",
			s:        "hello beautiful world",
			substr:   "beautiful",
			expected: true,
		},
		{
			name:     "substring not present",
			s:        "hello world",
			substr:   "xyz",
			expected: false,
		},
		{
			name:     "empty string",
			s:        "",
			substr:   "test",
			expected: false,
		},
		{
			name:     "empty substring",
			s:        "hello world",
			substr:   "",
			expected: true, // empty string is always found
		},
		{
			name:     "single char string",
			s:        "a",
			substr:   "a",
			expected: true, // single char can be found at position 0
		},
		{
			name:     "special characters in middle",
			s:        "hello@world!",
			substr:   "@",
			expected: true,
		},
		{
			name:     "multi-character substring in middle",
			s:        "start middle end",
			substr:   "middle",
			expected: true,
		},
		{
			name:     "overlapping occurrences",
			s:        "aaa",
			substr:   "aa",
			expected: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := containsMiddle(tt.s, tt.substr)
			assert.Equal(t, tt.expected, result)
		})
	}
}
