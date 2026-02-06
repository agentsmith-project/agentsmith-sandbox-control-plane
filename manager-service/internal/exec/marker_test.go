package exec

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestNewMarkerParser(t *testing.T) {
	parser := NewMarkerParser("__SBX_EXIT_CODE__", "stderr")

	assert.Equal(t, "__SBX_EXIT_CODE__", parser.markerKey)
	assert.Equal(t, "stderr", parser.stream)
}

func TestMarkerParser_Parse_ValidExitCode(t *testing.T) {
	parser := NewMarkerParser("__SBX_EXIT_CODE__", "stderr")

	stdout := "some output"
	stderr := "command output\n__SBX_EXIT_CODE__=0\n"

	exitCode, err := parser.Parse(stdout, stderr)

	require.NoError(t, err)
	assert.Equal(t, 0, exitCode)
}

func TestMarkerParser_Parse_NonZeroExitCode(t *testing.T) {
	parser := NewMarkerParser("__SBX_EXIT_CODE__", "stderr")

	stdout := "some output"
	stderr := "command output\n__SBX_EXIT_CODE__=127\n"

	exitCode, err := parser.Parse(stdout, stderr)

	require.NoError(t, err)
	assert.Equal(t, 127, exitCode)
}

func TestMarkerParser_Parse_NoMarker(t *testing.T) {
	parser := NewMarkerParser("__SBX_EXIT_CODE__", "stderr")

	stdout := "some output"
	stderr := "command output without marker"

	exitCode, err := parser.Parse(stdout, stderr)

	assert.Error(t, err)
	assert.Equal(t, -1, exitCode)
}

func TestMarkerParser_Parse_StdoutStream(t *testing.T) {
	parser := NewMarkerParser("__SBX_EXIT_CODE__", "stdout")

	stdout := "__SBX_EXIT_CODE__=1\n"
	stderr := "error output"

	exitCode, err := parser.Parse(stdout, stderr)

	require.NoError(t, err)
	assert.Equal(t, 1, exitCode)
}

func TestMarkerParser_ParseAndClean(t *testing.T) {
	parser := NewMarkerParser("__SBX_EXIT_CODE__", "stderr")

	stdout := "normal output"
	stderr := "error output\n__SBX_EXIT_CODE__=0\n"

	exitCode, cleanStdout, cleanStderr, err := parser.ParseAndClean(stdout, stderr)

	require.NoError(t, err)
	assert.Equal(t, 0, exitCode)
	assert.Equal(t, "normal output", cleanStdout)
	assert.Equal(t, "error output\n", cleanStderr)
}

func TestMarkerParser_ParseAndClean_StdoutStream(t *testing.T) {
	parser := NewMarkerParser("__SBX_EXIT_CODE__", "stdout")

	stdout := "output\n__SBX_EXIT_CODE__=1\n"
	stderr := "error output"

	exitCode, cleanStdout, cleanStderr, err := parser.ParseAndClean(stdout, stderr)

	require.NoError(t, err)
	assert.Equal(t, 1, exitCode)
	assert.Equal(t, "output\n", cleanStdout)
	assert.Equal(t, "error output", cleanStderr)
}

func TestParseExitCodeMarker_Valid(t *testing.T) {
	tests := []struct {
		name     string
		output   string
		marker   string
		expected int
	}{
		{
			name:     "exit code 0",
			output:   "__SBX_EXIT_CODE__=0\n",
			marker:   "__SBX_EXIT_CODE__",
			expected: 0,
		},
		{
			name:     "exit code 1",
			output:   "__SBX_EXIT_CODE__=1\n",
			marker:   "__SBX_EXIT_CODE__",
			expected: 1,
		},
		{
			name:     "exit code 127",
			output:   "__SBX_EXIT_CODE__=127\n",
			marker:   "__SBX_EXIT_CODE__",
			expected: 127,
		},
		{
			name:     "exit code 255",
			output:   "__SBX_EXIT_CODE__=255\n",
			marker:   "__SBX_EXIT_CODE__",
			expected: 255,
		},
		{
			name:     "marker with prefix",
			output:   "some output\n__SBX_EXIT_CODE__=0\n",
			marker:   "__SBX_EXIT_CODE__",
			expected: 0,
		},
		{
			name:     "marker at end without newline",
			output:   "output\n__SBX_EXIT_CODE__=42",
			marker:   "__SBX_EXIT_CODE__",
			expected: 42,
		},
		{
			name:     "custom marker key",
			output:   "CUSTOM_KEY=5\n",
			marker:   "CUSTOM_KEY",
			expected: 5,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			exitCode, err := ParseExitCodeMarker(tt.output, tt.marker)
			require.NoError(t, err)
			assert.Equal(t, tt.expected, exitCode)
		})
	}
}

func TestParseExitCodeMarker_NoMarker(t *testing.T) {
	exitCode, err := ParseExitCodeMarker("no marker here", "__SBX_EXIT_CODE__")

	assert.Error(t, err)
	assert.Equal(t, -1, exitCode)
}

func TestParseExitCodeMarker_InvalidExitCode(t *testing.T) {
	exitCode, err := ParseExitCodeMarker("__SBX_EXIT_CODE__=abc\n", "__SBX_EXIT_CODE__")

	assert.Error(t, err)
	assert.Equal(t, -1, exitCode)
}

func TestParseExitCodeMarker_MultipleMarkers_ReturnsLast(t *testing.T) {
	output := "__SBX_EXIT_CODE__=1\noutput\n__SBX_EXIT_CODE__=2\n"

	exitCode, err := ParseExitCodeMarker(output, "__SBX_EXIT_CODE__")

	require.NoError(t, err)
	assert.Equal(t, 2, exitCode)
}

func TestRemoveExitCodeMarker_RemovesLine(t *testing.T) {
	tests := []struct {
		name     string
		output   string
		marker   string
		expected string
	}{
		{
			name:     "remove marker line",
			output:   "output\n__SBX_EXIT_CODE__=0\n",
			marker:   "__SBX_EXIT_CODE__",
			expected: "output\n",
		},
		{
			name:     "remove marker line with prefix",
			output:   "output\nmore\n__SBX_EXIT_CODE__=1\n",
			marker:   "__SBX_EXIT_CODE__",
			expected: "output\nmore\n",
		},
		{
			name:     "no marker - unchanged",
			output:   "output\nmore\n",
			marker:   "__SBX_EXIT_CODE__",
			expected: "output\nmore\n",
		},
		{
			name:     "empty output",
			output:   "",
			marker:   "__SBX_EXIT_CODE__",
			expected: "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := RemoveExitCodeMarker(tt.output, tt.marker)
			assert.Equal(t, tt.expected, result)
		})
	}
}

func TestRemoveExitCodeMarker_CRLF(t *testing.T) {
	output := "output\r\n__SBX_EXIT_CODE__=0\r\n"

	result := RemoveExitCodeMarker(output, "__SBX_EXIT_CODE__")

	assert.Equal(t, "output\r\n", result)
}

func TestRemoveExitCodeMarker_MultipleMarkers(t *testing.T) {
	output := "output\n__SBX_EXIT_CODE__=1\nmore\n__SBX_EXIT_CODE__=0\n"

	result := RemoveExitCodeMarker(output, "__SBX_EXIT_CODE__")

	// Should remove the last marker line
	assert.Equal(t, "output\n__SBX_EXIT_CODE__=1\nmore\n", result)
}

func TestHasExitCodeMarker_True(t *testing.T) {
	output := "output\n__SBX_EXIT_CODE__=0\n"

	hasMarker := HasExitCodeMarker(output, "__SBX_EXIT_CODE__")

	assert.True(t, hasMarker)
}

func TestHasExitCodeMarker_False(t *testing.T) {
	output := "output without marker"

	hasMarker := HasExitCodeMarker(output, "__SBX_EXIT_CODE__")

	assert.False(t, hasMarker)
}

func TestMarkerPattern(t *testing.T) {
	pattern := MarkerPattern("__SBX_EXIT_CODE__")

	assert.Equal(t, "__SBX_EXIT_CODE__=<n>", pattern)
}

func TestValidateExitCode_Valid(t *testing.T) {
	validCodes := []int{0, 1, 127, 255}

	for _, code := range validCodes {
		t.Run(codeToString(code), func(t *testing.T) {
			err := ValidateExitCode(code)
			assert.NoError(t, err)
		})
	}
}

func TestValidateExitCode_Invalid(t *testing.T) {
	invalidCodes := []int{-1, -100, 256, 1000}

	for _, code := range invalidCodes {
		t.Run(codeToString(code), func(t *testing.T) {
			err := ValidateExitCode(code)
			assert.Error(t, err)
		})
	}
}

func codeToString(c int) string {
	switch c {
	case -1:
		return "minus-one"
	case -100:
		return "minus-hundred"
	case 256:
		return "256"
	case 1000:
		return "1000"
	default:
		return string(rune('0' + c))
	}
}
