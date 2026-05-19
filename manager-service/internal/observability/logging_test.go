package observability

import (
	"bytes"
	"log"
	"os"
	"strings"
	"testing"
)

// captureLog redirects Go's standard log output to a buffer for the duration
// of the test. It restores the original output on cleanup.
func captureLog(t *testing.T) *bytes.Buffer {
	t.Helper()
	buf := &bytes.Buffer{}
	old := log.Writer()
	log.SetOutput(buf)
	t.Cleanup(func() { log.SetOutput(old) })
	return buf
}

// ---- DefaultLogger ----------------------------------------------------------

func TestDefaultLogger_DebugDisabled_SuppressesDebug(t *testing.T) {
	buf := captureLog(t)
	logger := NewDefaultLogger(false)
	logger.Debug("secret message: %s", "should not appear")
	if strings.Contains(buf.String(), "secret message") {
		t.Errorf("Debug output should be suppressed when debug=false, got: %s", buf.String())
	}
}

func TestDefaultLogger_DebugEnabled_EmitsDebug(t *testing.T) {
	buf := captureLog(t)
	logger := NewDefaultLogger(true)
	logger.Debug("debug payload: %s", "visible")
	if !strings.Contains(buf.String(), "debug payload: visible") {
		t.Errorf("Debug not emitted when debug=true; got: %s", buf.String())
	}
	if !strings.Contains(buf.String(), "[DEBUG]") {
		t.Errorf("Debug log missing [DEBUG] prefix; got: %s", buf.String())
	}
}

func TestDefaultLogger_Info_AlwaysLogs(t *testing.T) {
	buf := captureLog(t)
	logger := NewDefaultLogger(false)
	logger.Info("info payload: %s", "important")
	if !strings.Contains(buf.String(), "info payload: important") {
		t.Errorf("Info not logged; got: %s", buf.String())
	}
	if !strings.Contains(buf.String(), "[INFO]") {
		t.Errorf("Info log missing [INFO] prefix; got: %s", buf.String())
	}
}

func TestDefaultLogger_Warn_AlwaysLogs(t *testing.T) {
	buf := captureLog(t)
	logger := NewDefaultLogger(false)
	logger.Warn("warn event: %d", 42)
	if !strings.Contains(buf.String(), "warn event: 42") {
		t.Errorf("Warn not logged; got: %s", buf.String())
	}
	if !strings.Contains(buf.String(), "[WARN]") {
		t.Errorf("Warn log missing [WARN] prefix; got: %s", buf.String())
	}
}

func TestDefaultLogger_Error_AlwaysLogs(t *testing.T) {
	buf := captureLog(t)
	logger := NewDefaultLogger(false)
	logger.Error("error occurred: %v", "boom")
	if !strings.Contains(buf.String(), "error occurred: boom") {
		t.Errorf("Error not logged; got: %s", buf.String())
	}
	if !strings.Contains(buf.String(), "[ERROR]") {
		t.Errorf("Error log missing [ERROR] prefix; got: %s", buf.String())
	}
}

func TestRedactLogValueRemovesCredentialLikeMaterial(t *testing.T) {
	longOutput := strings.Repeat("x", 900)
	input := "dependency failed: Authorization: Bearer raw-token-123 token=abc123 password=\"p@ss\" stderr=" + longOutput

	got := RedactLogValue(input)

	for _, leaked := range []string{"raw-token-123", "abc123", "p@ss", longOutput} {
		if strings.Contains(got, leaked) {
			t.Fatalf("RedactLogValue leaked %q in %q", leaked, got)
		}
	}
	if !strings.Contains(got, "[REDACTED]") {
		t.Fatalf("RedactLogValue must mark redacted material, got %q", got)
	}
	if len(got) > 640 {
		t.Fatalf("RedactLogValue should bound log payload length, got %d chars: %q", len(got), got)
	}
}

func TestDefaultLogger_Debug_NotLoggedWhenDisabled_EvenIfOtherLogsAre(t *testing.T) {
	buf := captureLog(t)
	logger := NewDefaultLogger(false)
	logger.Info("info line")
	logger.Debug("debug line") // should be suppressed
	logger.Warn("warn line")
	if strings.Contains(buf.String(), "debug line") {
		t.Errorf("debug line appeared despite debug=false; log: %s", buf.String())
	}
	if !strings.Contains(buf.String(), "info line") || !strings.Contains(buf.String(), "warn line") {
		t.Errorf("expected info and warn lines in log; got: %s", buf.String())
	}
}

// ---- Global logger ----------------------------------------------------------

func TestSetLogger_ReplacesGlobalLogger(t *testing.T) {
	original := GetLogger()
	t.Cleanup(func() { SetLogger(original) })

	custom := NewDefaultLogger(true)
	SetLogger(custom)
	if GetLogger() != custom {
		t.Error("GetLogger() did not return the logger set via SetLogger()")
	}
}

func TestGetLogger_ReturnsCurrentGlobal(t *testing.T) {
	original := GetLogger()
	if original == nil {
		t.Error("GetLogger() returned nil; global logger should always be non-nil")
	}
}

func TestGlobalFunctions_DelegateToCurrentLogger(t *testing.T) {
	buf := captureLog(t)
	original := GetLogger()
	t.Cleanup(func() { SetLogger(original) })

	// Set a debug-enabled global logger so that Debug is also emitted
	SetLogger(NewDefaultLogger(true))

	Debug("global-debug")
	Info("global-info")
	Warn("global-warn")
	Error("global-error")

	output := buf.String()
	for _, token := range []string{"global-debug", "global-info", "global-warn", "global-error"} {
		if !strings.Contains(output, token) {
			t.Errorf("global %s not found in log output; got: %s", token, output)
		}
	}
}

// ---- InitLogging ------------------------------------------------------------

func TestInitLogging_LogLevelDebug_EnablesDebug(t *testing.T) {
	original := GetLogger()
	t.Cleanup(func() {
		SetLogger(original)
		_ = os.Unsetenv("LOG_LEVEL")
	})

	_ = os.Setenv("LOG_LEVEL", "debug")
	InitLogging()

	buf := captureLog(t)
	Debug("should-appear")
	if !strings.Contains(buf.String(), "should-appear") {
		t.Errorf("After InitLogging with LOG_LEVEL=debug, Debug should emit; got: %s", buf.String())
	}
}

func TestInitLogging_DebugEnvTrue_EnablesDebug(t *testing.T) {
	original := GetLogger()
	t.Cleanup(func() {
		SetLogger(original)
		_ = os.Unsetenv("DEBUG")
	})

	_ = os.Setenv("DEBUG", "true")
	InitLogging()

	buf := captureLog(t)
	Debug("via-debug-env")
	if !strings.Contains(buf.String(), "via-debug-env") {
		t.Errorf("After InitLogging with DEBUG=true, Debug should emit; got: %s", buf.String())
	}
}

func TestInitLogging_Default_SuppressesDebug(t *testing.T) {
	original := GetLogger()
	t.Cleanup(func() {
		SetLogger(original)
		_ = os.Unsetenv("LOG_LEVEL")
		_ = os.Unsetenv("DEBUG")
	})

	_ = os.Unsetenv("LOG_LEVEL")
	_ = os.Unsetenv("DEBUG")
	InitLogging()

	buf := captureLog(t)
	Debug("hidden-debug")
	if strings.Contains(buf.String(), "hidden-debug") {
		t.Errorf("After InitLogging with no debug env, Debug should be suppressed; got: %s", buf.String())
	}
}
