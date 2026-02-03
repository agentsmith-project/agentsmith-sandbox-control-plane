package observability

import (
	"log"
	"os"
)

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

// InitLogging initializes logging based on environment variables
func InitLogging() {
	// Check for debug mode
	debug := os.Getenv("DEBUG") == "true" || os.Getenv("LOG_LEVEL") == "debug"
	if debug {
		globalLogger = NewDefaultLogger(true)
	}
}
