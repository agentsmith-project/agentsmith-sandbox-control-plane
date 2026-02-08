package audit

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestLogger_LogEvent(t *testing.T) {
	logger := NewLogger()

	event := &Event{
		Type:      EventAuthAttempt,
		UserID:    "test-user",
		SessionID: "test-session",
		Success:   true,
		Details:   map[string]interface{}{"key": "value"},
		Timestamp: time.Now(),
		RequestID: "req-123",
		IP:        "127.0.0.1",
	}

	logger.LogEvent(event)
}

func TestLogger_LogAuthAttempt(t *testing.T) {
	logger := NewLogger()

	logger.LogAuthAttempt("test-user", "req-123", "127.0.0.1", true, map[string]interface{}{
		"authMethod": "jwt",
	})
}

func TestLogger_LogSessionCreate(t *testing.T) {
	logger := NewLogger()

	logger.LogSessionCreate("test-user", "test-session", "req-123", "127.0.0.1", true, map[string]interface{}{
		"authMethod": "service_key",
	})
}

func TestNewEventFromRequest(t *testing.T) {
	// Test with no user context
	req := httptest.NewRequest("GET", "/test", nil)
	req.Header.Set("X-Request-Id", "req-123")
	req.RemoteAddr = "127.0.0.1:1234"

	getUserContext := func(r *http.Request) (string, string) {
		return "", ""
	}

	event := NewEventFromRequest(req, EventAuthAttempt, getUserContext)

	if event.Type != EventAuthAttempt {
		t.Errorf("Expected event type %s, got %s", EventAuthAttempt, event.Type)
	}
	if event.RequestID != "req-123" {
		t.Errorf("Expected request ID 'req-123', got '%s'", event.RequestID)
	}
	if event.IP != "127.0.0.1:1234" {
		t.Errorf("Expected IP '127.0.0.1:1234', got '%s'", event.IP)
	}
}

func TestGetClientIP(t *testing.T) {
	tests := []struct {
		name           string
		headers        map[string]string
		remoteAddr     string
		expectedIP     string
	}{
		{
			name:       "remote addr only",
			remoteAddr: "192.168.1.1:1234",
			expectedIP: "192.168.1.1:1234",
		},
		{
			name: "X-Forwarded-For",
			headers: map[string]string{
				"X-Forwarded-For": "10.0.0.1, 192.168.1.1",
			},
			remoteAddr: "192.168.1.1:1234",
			expectedIP: "10.0.0.1, 192.168.1.1",
		},
		{
			name: "X-Real-IP",
			headers: map[string]string{
				"X-Real-IP": "10.0.0.1",
			},
			remoteAddr: "192.168.1.1:1234",
			expectedIP: "10.0.0.1",
		},
		{
			name:       "X-Forwarded-For takes precedence",
			headers: map[string]string{
				"X-Forwarded-For": "10.0.0.1",
				"X-Real-IP":       "20.0.0.1",
			},
			remoteAddr: "192.168.1.1:1234",
			expectedIP: "10.0.0.1",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := httptest.NewRequest("GET", "/test", nil)
			req.RemoteAddr = tt.remoteAddr

			for key, value := range tt.headers {
				req.Header.Set(key, value)
			}

			ip := getClientIP(req)

			if ip != tt.expectedIP {
				t.Errorf("Expected IP '%s', got '%s'", tt.expectedIP, ip)
			}
		})
	}
}

func TestGetRequestIdFromRequest(t *testing.T) {
	tests := []struct {
		name     string
		headers  map[string]string
		expected string
	}{
		{
			name:     "no headers",
			expected: "",
		},
		{
			name: "X-Request-Id",
			headers: map[string]string{
				"X-Request-Id": "req-123",
			},
			expected: "req-123",
		},
		{
			name: "X-Request-ID",
			headers: map[string]string{
				"X-Request-ID": "req-456",
			},
			expected: "req-456",
		},
		{
			name: "Request-Id",
			headers: map[string]string{
				"Request-Id": "req-789",
			},
			expected: "req-789",
		},
		{
			name: "X-Request-Id takes precedence",
			headers: map[string]string{
				"X-Request-ID": "req-456",
				"X-Request-Id": "req-123",
				"Request-Id":  "req-789",
			},
			expected: "req-123",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := httptest.NewRequest("GET", "/test", nil)

			for key, value := range tt.headers {
				req.Header.Set(key, value)
			}

			id := getRequestIdFromRequest(req)

			if id != tt.expected {
				t.Errorf("Expected request ID '%s', got '%s'", tt.expected, id)
			}
		})
	}
}