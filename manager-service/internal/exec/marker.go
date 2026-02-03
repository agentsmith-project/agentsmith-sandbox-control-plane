package exec

import (
	"fmt"
	"strconv"
	"strings"
)

// MarkerParser parses exit code markers from command output
type MarkerParser struct {
	markerKey string
	stream    string
}

// NewMarkerParser creates a new marker parser
func NewMarkerParser(markerKey, stream string) *MarkerParser {
	return &MarkerParser{
		markerKey: markerKey,
		stream:    stream,
	}
}

// Parse extracts the exit code from the output
func (m *MarkerParser) Parse(stdout, stderr string) (int, error) {
	output := stdout
	if m.stream == "stderr" {
		output = stderr
	}

	return ParseExitCodeMarker(output, m.markerKey)
}

// ParseAndClean extracts the exit code and removes the marker from output
func (m *MarkerParser) ParseAndClean(stdout, stderr string) (int, string, string, error) {
	exitCode, err := m.Parse(stdout, stderr)

	cleanStdout := stdout
	cleanStderr := stderr

	if m.stream == "stderr" {
		cleanStderr = RemoveExitCodeMarker(stderr, m.markerKey)
	} else {
		cleanStdout = RemoveExitCodeMarker(stdout, m.markerKey)
	}

	return exitCode, cleanStdout, cleanStderr, err
}

// ParseExitCodeMarker extracts the exit code from a marker in output
// The marker format is: __SBX_EXIT_CODE__=<n>
func ParseExitCodeMarker(output, markerKey string) (int, error) {
	// Find the marker in output
	markerPrefix := markerKey + "="
	idx := findLastMarkerStart(output, markerPrefix)
	if idx < 0 {
		return -1, fmt.Errorf("exit code marker not found")
	}

	// Extract the number after the marker
	exitCodeStr := output[idx+len(markerPrefix):]

	// The exit code should be at the end of a line
	// Find the end of the line (or end of string)
	lineEnd := strings.IndexAny(exitCodeStr, "\n\r")
	if lineEnd >= 0 {
		exitCodeStr = exitCodeStr[:lineEnd]
	}

	// Parse the exit code
	exitCode, err := strconv.Atoi(strings.TrimSpace(exitCodeStr))
	if err != nil {
		return -1, fmt.Errorf("invalid exit code in marker: %w", err)
	}

	return exitCode, nil
}

// RemoveExitCodeMarker removes the exit code marker from output
func RemoveExitCodeMarker(output, markerKey string) string {
	markerPrefix := markerKey + "="
	idx := findLastMarkerStart(output, markerPrefix)
	if idx < 0 {
		return output
	}

	// Find the start of the line containing the marker
	lineStart := idx
	for lineStart > 0 && output[lineStart-1] != '\n' && output[lineStart-1] != '\r' {
		lineStart--
	}

	// Find the end of the marker line
	lineEnd := idx
	for lineEnd < len(output) && output[lineEnd] != '\n' && output[lineEnd] != '\r' {
		lineEnd++
	}

	// Skip past the line ending
	if lineEnd < len(output) && (output[lineEnd] == '\n' || output[lineEnd] == '\r') {
		lineEnd++
		if lineEnd < len(output) && output[lineEnd-1] == '\r' && output[lineEnd] == '\n' {
			// Handle Windows CRLF
			lineEnd++
		}
	}

	// Remove the marker line
	return output[:lineStart] + output[lineEnd:]
}

// findLastMarkerStart finds the start of the last marker in output
func findLastMarkerStart(output, markerPrefix string) int {
	lastIdx := -1
	searchFrom := 0

	for {
		idx := strings.Index(output[searchFrom:], markerPrefix)
		if idx < 0 {
			break
		}

		// Adjust to absolute position
		absIdx := searchFrom + idx

		// Verify this looks like a marker (should be at line start or after whitespace)
		if absIdx == 0 || output[absIdx-1] == ' ' || output[absIdx-1] == '\t' ||
			output[absIdx-1] == '\n' || output[absIdx-1] == '\r' {
			lastIdx = absIdx
		}

		searchFrom = absIdx + len(markerPrefix)
	}

	return lastIdx
}

// HasExitCodeMarker checks if output contains an exit code marker
func HasExitCodeMarker(output, markerKey string) bool {
	markerPrefix := markerKey + "="
	return findLastMarkerStart(output, markerPrefix) >= 0
}

// MarkerPattern returns the marker pattern for logging
func MarkerPattern(markerKey string) string {
	return markerKey + "=<n>"
}

// ValidateExitCode validates that an exit code is in the valid range
func ValidateExitCode(code int) error {
	if code < 0 || code > 255 {
		return fmt.Errorf("invalid exit code: %d (must be 0-255)", code)
	}
	return nil
}
