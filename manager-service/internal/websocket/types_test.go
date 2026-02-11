package websocket

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestTypeSignalConstant(t *testing.T) {
	assert.Equal(t, "signal", TypeSignal)
}

func TestSignalPayloadSerialization(t *testing.T) {
	tests := []struct {
		name     string
		payload  SignalPayload
		expected string
	}{
		{
			name: "SIGINT signal",
			payload: SignalPayload{
				SandboxID: "sandbox-123",
				Signal:    "SIGINT",
			},
			expected: `{"sandbox_id":"sandbox-123","signal":"SIGINT"}`,
		},
		{
			name: "SIGTERM signal",
			payload: SignalPayload{
				SandboxID: "test-sandbox",
				Signal:    "SIGTERM",
			},
			expected: `{"sandbox_id":"test-sandbox","signal":"SIGTERM"}`,
		},
		{
			name: "SIGKILL signal",
			payload: SignalPayload{
				SandboxID: "sandbox-456",
				Signal:    "SIGKILL",
			},
			expected: `{"sandbox_id":"sandbox-456","signal":"SIGKILL"}`,
		},
		{
			name: "SIGHUP signal",
			payload: SignalPayload{
				SandboxID: "sandbox-789",
				Signal:    "SIGHUP",
			},
			expected: `{"sandbox_id":"sandbox-789","signal":"SIGHUP"}`,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Test serialization
			data, err := json.Marshal(tt.payload)
			require.NoError(t, err)
			assert.JSONEq(t, tt.expected, string(data))

			// Test deserialization
			var decoded SignalPayload
			err = json.Unmarshal(data, &decoded)
			require.NoError(t, err)
			assert.Equal(t, tt.payload, decoded)
		})
	}
}

func TestSignalPayloadDeserialization(t *testing.T) {
	tests := []struct {
		name     string
		json     string
		expected SignalPayload
	}{
		{
			name: "valid signal payload",
			json:  `{"sandbox_id":"test-123","signal":"SIGINT"}`,
			expected: SignalPayload{
				SandboxID: "test-123",
				Signal:    "SIGINT",
			},
		},
		{
			name: "missing sandbox_id results in empty string",
			json:  `{"signal":"SIGTERM"}`,
			expected: SignalPayload{
				SandboxID: "",
				Signal:    "SIGTERM",
			},
		},
		{
			name: "missing signal results in empty string",
			json:  `{"sandbox_id":"test-123"}`,
			expected: SignalPayload{
				SandboxID: "test-123",
				Signal:    "",
			},
		},
		{
			name: "empty values",
			json: `{"sandbox_id":"","signal":""}`,
			expected: SignalPayload{
				SandboxID: "",
				Signal:    "",
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var payload SignalPayload
			err := json.Unmarshal([]byte(tt.json), &payload)
			require.NoError(t, err)
			assert.Equal(t, tt.expected, payload)
		})
	}
}

func TestMessage_WithSignal(t *testing.T) {
	tests := []struct {
		name     string
		message  Message
		expected string
	}{
		{
			name: "signal message with SIGINT",
			message: Message{
				Type: TypeSignal,
				Data: json.RawMessage(`{"sandbox_id":"sandbox-123","signal":"SIGINT"}`),
			},
			expected: `{"type":"signal","data":{"sandbox_id":"sandbox-123","signal":"SIGINT"}}`,
		},
		{
			name: "signal message with SIGTERM",
			message: Message{
				Type: TypeSignal,
				Data: json.RawMessage(`{"sandbox_id":"test-sandbox","signal":"SIGTERM"}`),
			},
			expected: `{"type":"signal","data":{"sandbox_id":"test-sandbox","signal":"SIGTERM"}}`,
		},
		{
			name: "signal message with SIGKILL",
			message: Message{
				Type: TypeSignal,
				Data: json.RawMessage(`{"sandbox_id":"kill-target","signal":"SIGKILL"}`),
			},
			expected: `{"type":"signal","data":{"sandbox_id":"kill-target","signal":"SIGKILL"}}`,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Test serialization
			data, err := json.Marshal(tt.message)
			require.NoError(t, err)
			assert.JSONEq(t, tt.expected, string(data))

			// Test deserialization
			var decoded Message
			err = json.Unmarshal(data, &decoded)
			require.NoError(t, err)
			assert.Equal(t, tt.message.Type, decoded.Type)

			// Decode the Data field to SignalPayload
			var payload SignalPayload
			err = json.Unmarshal(decoded.Data, &payload)
			require.NoError(t, err)

			// Decode original message's Data to compare
			var originalPayload SignalPayload
			err = json.Unmarshal(tt.message.Data, &originalPayload)
			require.NoError(t, err)
			assert.Equal(t, originalPayload, payload)
		})
	}
}

func TestSignalPayload_RoundTrip(t *testing.T) {
	original := SignalPayload{
		SandboxID: "test-sandbox-roundtrip",
		Signal:    "SIGHUP",
	}

	// Marshal
	data, err := json.Marshal(original)
	require.NoError(t, err)

	// Unmarshal
	var decoded SignalPayload
	err = json.Unmarshal(data, &decoded)
	require.NoError(t, err)

	// Verify
	assert.Equal(t, original.SandboxID, decoded.SandboxID)
	assert.Equal(t, original.Signal, decoded.Signal)
}

func TestMessage_WithSignal_RoundTrip(t *testing.T) {
	payload := SignalPayload{
		SandboxID: "sandbox-roundtrip",
		Signal:    "SIGUSR1",
	}

	payloadData, err := json.Marshal(payload)
	require.NoError(t, err)

	original := Message{
		Type: TypeSignal,
		Data: payloadData,
	}

	// Marshal
	data, err := json.Marshal(original)
	require.NoError(t, err)

	// Unmarshal
	var decoded Message
	err = json.Unmarshal(data, &decoded)
	require.NoError(t, err)

	// Verify type
	assert.Equal(t, TypeSignal, decoded.Type)

	// Unmarshal data to SignalPayload
	var decodedPayload SignalPayload
	err = json.Unmarshal(decoded.Data, &decodedPayload)
	require.NoError(t, err)

	// Verify payload
	assert.Equal(t, payload.SandboxID, decodedPayload.SandboxID)
	assert.Equal(t, payload.Signal, decodedPayload.Signal)
}
