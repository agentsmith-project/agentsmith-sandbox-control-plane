package observability

import (
	"fmt"
	"log"
	"os"
	"regexp"
	"strings"
)

const maxRedactedLogValueLength = 512

var logValueRedactionPatterns = []*regexp.Regexp{
	regexp.MustCompile(`(?i)(authorization\s*:\s*bearer\s+)[^ \t,;]+`),
	regexp.MustCompile(`(?i)(\bbearer\s+)[A-Za-z0-9._~+/=-]+`),
	regexp.MustCompile(`(?i)(\b(?:token|access_token|refresh_token|id_token|api[_-]?key|secret|password|credential|credentials|service[_-]?key|client[_-]?secret)\b\s*[:=]\s*)("[^"]*"|'[^']*'|[^ \t,;]+)`),
	regexp.MustCompile(`(?i)("?(?:token|access_token|refresh_token|id_token|api[_-]?key|secret|password|credential|credentials|service[_-]?key|client[_-]?secret)"?\s*:\s*)("[^"]*"|[^,}\s]+)`),
	regexp.MustCompile(`([a-z][a-z0-9+.-]*://)[^/@\s:]+:[^/@\s]+@`),
}

// Logger is the interface for logging
type Logger interface {
	Debug(msg string, args ...interface{})
	Info(msg string, args ...interface{})
	Warn(msg string, args ...interface{})
	Error(msg string, args ...interface{})
}

// DefaultLogger is the default logger implementation
type DefaultLogger struct {
	debug bool
}

// NewDefaultLogger creates a new default logger
func NewDefaultLogger(debug bool) *DefaultLogger {
	return &DefaultLogger{debug: debug}
}

// Debug logs a debug message
func (l *DefaultLogger) Debug(msg string, args ...interface{}) {
	if l.debug {
		log.Printf("[DEBUG] "+msg, args...)
	}
}

// Info logs an info message
func (l *DefaultLogger) Info(msg string, args ...interface{}) {
	log.Printf("[INFO] "+msg, args...)
}

// Warn logs a warning message
func (l *DefaultLogger) Warn(msg string, args ...interface{}) {
	log.Printf("[WARN] "+msg, args...)
}

// Error logs an error message
func (l *DefaultLogger) Error(msg string, args ...interface{}) {
	log.Printf("[ERROR] "+msg, args...)
}

// Global logger instance
var globalLogger Logger = NewDefaultLogger(false)

// SetLogger sets the global logger
func SetLogger(logger Logger) {
	globalLogger = logger
}

// GetLogger returns the global logger
func GetLogger() Logger {
	return globalLogger
}

// Debug logs a debug message
func Debug(msg string, args ...interface{}) {
	globalLogger.Debug(msg, args...)
}

// Info logs an info message
func Info(msg string, args ...interface{}) {
	globalLogger.Info(msg, args...)
}

// Warn logs a warning message
func Warn(msg string, args ...interface{}) {
	globalLogger.Warn(msg, args...)
}

// Error logs an error message
func Error(msg string, args ...interface{}) {
	globalLogger.Error(msg, args...)
}

// RedactLogValue formats an error/log value for service logs without exposing
// credential-like material or unbounded command/dependency output.
func RedactLogValue(value interface{}) string {
	text := fmt.Sprint(value)
	text = strings.ReplaceAll(text, "\r", " ")
	text = strings.ReplaceAll(text, "\n", " ")
	text = strings.Join(strings.Fields(text), " ")

	for _, pattern := range logValueRedactionPatterns {
		text = pattern.ReplaceAllString(text, "${1}[REDACTED]")
	}

	runes := []rune(text)
	if len(runes) > maxRedactedLogValueLength {
		text = string(runes[:maxRedactedLogValueLength]) + "...[TRUNCATED]"
	}
	return text
}

// InitLogging initializes logging based on environment variables
func InitLogging() {
	// Check for debug mode
	debug := os.Getenv("DEBUG") == "true" || os.Getenv("LOG_LEVEL") == "debug"
	if debug {
		globalLogger = NewDefaultLogger(true)
	}
}

// InitLoggerForTest initializes the logger for testing.
// It enables debug mode to help with troubleshooting.
func InitLoggerForTest() {
	globalLogger = NewDefaultLogger(true)
}
