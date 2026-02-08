package audit

import (
	"time"
)

type EventType string

const (
	EventAuthAttempt   EventType = "auth_attempt"
	EventSessionCreate  EventType = "session_create"
	EventSessionDelete  EventType = "session_delete"
	EventSessionAccess  EventType = "session_access"
	EventFileUpload     EventType = "file_upload"
	EventFileDownload   EventType = "file_download"
	EventCommandExec    EventType = "command_exec"
)

type Event struct {
	Type      EventType
	UserID    string
	SessionID string
	Success   bool
	Details   map[string]interface{}
	Timestamp time.Time
	RequestID string
	IP        string
}