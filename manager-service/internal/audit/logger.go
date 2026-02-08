package audit

import (
	"encoding/json"
	"log"
	"net/http"
	"sync"
	"time"
)

// Logger writes audit events to the log
type Logger struct {
	mu sync.Mutex
}

// NewLogger creates a new audit logger
func NewLogger() *Logger {
	return &Logger{}
}

// LogEvent creates and logs an audit event
func (l *Logger) LogEvent(event *Event) {
	l.mu.Lock()
	defer l.mu.Unlock()

	// Add timestamp if not set
	if event.Timestamp.IsZero() {
		event.Timestamp = time.Now()
	}

	// Convert event to JSON for structured logging
	eventJSON, err := json.Marshal(event)
	if err != nil {
		log.Printf("[ERROR] Failed to marshal audit event: %v", err)
		return
	}

	log.Printf("[AUDIT] %s", eventJSON)
}

// LogAuthAttempt logs authentication attempts
func (l *Logger) LogAuthAttempt(userID, requestID, ip string, success bool, details map[string]interface{}) {
	event := &Event{
		Type:      EventAuthAttempt,
		UserID:    userID,
		Success:   success,
		RequestID: requestID,
		IP:        ip,
		Details:   details,
	}
	l.LogEvent(event)
}

// LogSessionCreate logs session creation events
func (l *Logger) LogSessionCreate(userID, sessionID, requestID, ip string, success bool, details map[string]interface{}) {
	event := &Event{
		Type:      EventSessionCreate,
		UserID:    userID,
		SessionID: sessionID,
		Success:   success,
		RequestID: requestID,
		IP:        ip,
		Details:   details,
	}
	l.LogEvent(event)
}

// LogSessionDelete logs session deletion events
func (l *Logger) LogSessionDelete(userID, sessionID, requestID, ip string, success bool, details map[string]interface{}) {
	event := &Event{
		Type:      EventSessionDelete,
		UserID:    userID,
		SessionID: sessionID,
		Success:   success,
		RequestID: requestID,
		IP:        ip,
		Details:   details,
	}
	l.LogEvent(event)
}

// LogSessionAccess logs session access events
func (l *Logger) LogSessionAccess(userID, sessionID, requestID, ip string, success bool, details map[string]interface{}) {
	event := &Event{
		Type:      EventSessionAccess,
		UserID:    userID,
		SessionID: sessionID,
		Success:   success,
		RequestID: requestID,
		IP:        ip,
		Details:   details,
	}
	l.LogEvent(event)
}

// LogFileUpload logs file upload events
func (l *Logger) LogFileUpload(userID, sessionID, requestID, ip string, success bool, details map[string]interface{}) {
	event := &Event{
		Type:      EventFileUpload,
		UserID:    userID,
		SessionID: sessionID,
		Success:   success,
		RequestID: requestID,
		IP:        ip,
		Details:   details,
	}
	l.LogEvent(event)
}

// LogFileDownload logs file download events
func (l *Logger) LogFileDownload(userID, sessionID, requestID, ip string, success bool, details map[string]interface{}) {
	event := &Event{
		Type:      EventFileDownload,
		UserID:    userID,
		SessionID: sessionID,
		Success:   success,
		RequestID: requestID,
		IP:        ip,
		Details:   details,
	}
	l.LogEvent(event)
}

// LogCommandExec logs command execution events
func (l *Logger) LogCommandExec(userID, sessionID, requestID, ip string, success bool, details map[string]interface{}) {
	event := &Event{
		Type:      EventCommandExec,
		UserID:    userID,
		SessionID: sessionID,
		Success:   success,
		RequestID: requestID,
		IP:        ip,
		Details:   details,
	}
	l.LogEvent(event)
}

// NewEventFromRequest creates a new event with common fields from the HTTP request
func NewEventFromRequest(r *http.Request, eventType EventType, getUserContext func(*http.Request) (string, string)) *Event {
	event := &Event{
		Type:      eventType,
		UserID:    "",
		SessionID: "",
		RequestID: getRequestIdFromRequest(r),
		IP:        getClientIP(r),
		Timestamp: time.Now(),
		Details:   make(map[string]interface{}),
	}

	// Extract user context if function provided
	if getUserContext != nil {
		userID, sessionID := getUserContext(r)
		event.UserID = userID
		event.SessionID = sessionID
	}

	return event
}

// Helper functions to extract information from requests

func getRequestIdFromRequest(r *http.Request) string {
	// Extract request ID from headers
	if id := r.Header.Get("X-Request-Id"); id != "" {
		return id
	}
	if id := r.Header.Get("X-Request-ID"); id != "" {
		return id
	}
	if id := r.Header.Get("Request-Id"); id != "" {
		return id
	}
	return ""
}

func getClientIP(r *http.Request) string {
	// Get client IP address, respecting X-Forwarded-For if present
	ip := r.Header.Get("X-Forwarded-For")
	if ip == "" {
		ip = r.Header.Get("X-Real-IP")
	}
	if ip == "" {
		ip = r.RemoteAddr
	}
	return ip
}

// Public functions for testing
func GetRequestIdFromRequest(r *http.Request) string {
	return getRequestIdFromRequest(r)
}

func GetClientIP(r *http.Request) string {
	return getClientIP(r)
}