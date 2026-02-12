package exec

import (
	"fmt"
	"strings"
)

// Wrapper builds a shell command wrapper that includes:
// 1. Setting environment variables
// 2. Changing to the working directory
// 3. Executing the user command
// 4. Outputting an exit code marker
type Wrapper struct {
	ShellBin       string
	ShellArgs      []string
	ExitCodeMarker string
	MarkerStream   string
}

// NewWrapper creates a new command wrapper
func NewWrapper(shellBin string, shellArgs []string, exitCodeMarker, markerStream string) *Wrapper {
	return &Wrapper{
		ShellBin:       shellBin,
		ShellArgs:      shellArgs,
		ExitCodeMarker: exitCodeMarker,
		MarkerStream:   markerStream,
	}
}

// BuildCommand builds the wrapped command string.
//
// The user command is wrapped in a subshell so that the exit code marker
// is ALWAYS written, regardless of whether the user command succeeds or fails.
// The pattern is:
//
//	cd /workspace && ( user_cmd ; _ec=$? ; echo "MARKER=$_ec" >&2 ; exit $_ec )
//
// This ensures:
//  1. cd must succeed (&&)
//  2. user_cmd runs; its exit code is captured into _ec
//  3. The marker echo always fires (uses ; not &&)
//  4. exit $_ec propagates the original exit code to the caller
func (w *Wrapper) BuildCommand(env map[string]string, workdir string, userCmd []string) string {
	var preamble []string

	// Set environment variables
	if len(env) > 0 {
		envExports := make([]string, 0, len(env))
		for k, v := range env {
			envExports = append(envExports, fmt.Sprintf("export %s=%s", k, shellEscape(v)))
		}
		preamble = append(preamble, strings.Join(envExports, " && "))
	}

	// Change to workdir
	preamble = append(preamble, fmt.Sprintf("cd %s", shellEscape(workdir)))

	// Build the user command + marker subshell.
	// Using ; ensures the marker fires even when the user command fails.
	userCmdStr := w.buildUserCommand(userCmd)
	markerCmd := w.buildMarkerCommand()
	subshell := fmt.Sprintf("( %s ; _ec=$? ; %s ; exit $_ec )", userCmdStr, markerCmd)

	preamble = append(preamble, subshell)

	// Preamble parts (env, cd) use && so they abort on failure.
	// The subshell is the last element and always emits the marker.
	return strings.Join(preamble, " && ")
}

// buildUserCommand builds the user command part of the wrapper
func (w *Wrapper) buildUserCommand(cmd []string) string {
	if len(cmd) == 0 {
		return "true" // no-op command
	}

	// Check if this is a command with string argument (e.g., sh -c "...")
	if isCommandWithStringArg(cmd) {
		return buildStringArgCommand(cmd)
	}

	// Regular command - quote each argument
	escapedArgs := make([]string, len(cmd))
	for i, arg := range cmd {
		escapedArgs[i] = shellEscape(arg)
	}
	return strings.Join(escapedArgs, " ")
}

// buildMarkerCommand builds the command that outputs the exit code marker.
// The marker uses $_ec which is set by the caller (BuildCommand subshell).
func (w *Wrapper) buildMarkerCommand() string {
	if w.MarkerStream == "stdout" {
		return fmt.Sprintf("echo \"%s=$_ec\"", w.ExitCodeMarker)
	}
	// Default to stderr - use = separator for parsing
	return fmt.Sprintf("echo \"%s=$_ec\" >&2", w.ExitCodeMarker)
}

// GetCommandArgs returns the shell command with the wrapped command as argument
func (w *Wrapper) GetCommandArgs(env map[string]string, workdir string, userCmd []string) []string {
	wrappedCmd := w.BuildCommand(env, workdir, userCmd)
	args := append([]string{}, w.ShellArgs...)
	args = append(args, wrappedCmd)
	return append([]string{w.ShellBin}, args...)
}

// shellEscape escapes a string for safe use in shell commands.
//
// This function prevents command injection by:
// 1. Wrapping the entire string in single quotes
// 2. Escaping any single quotes within the string using '\'' sequence
//
// Within single quotes, all characters are treated literally except single quotes themselves.
// To include a single quote, we end the single-quoted string, add an escaped single quote
// (using backslash), and restart the single-quoted string.
//
// Examples:
//   "hello"     → "'hello'"
//   "it's"      → "'it'\''s'"
//   "; rm -rf /" → "'; rm -rf /'"
//
// This approach prevents injection via:
// - Command separators (;, |, &, &&, ||)
// - Command substitution ($(), backticks)
// - Variable expansion ($VAR)
// - Newlines and other special characters
func shellEscape(s string) string {
	if s == "" {
		return "''"
	}
	// Replace single quotes with '\'' (end quote, escaped quote, start quote)
	// This is the standard shell escaping pattern: 'end' \' start '
	return "'" + strings.ReplaceAll(s, "'", "'\\''") + "'"
}

// escapeForDoubleQuotes escapes a string for use in shell double quotes
// Preserves $ for variable expansion and ` for command substitution
func escapeForDoubleQuotes(s string) string {
	// Escape backslashes first (must be first to avoid double-escaping)
	escaped := strings.ReplaceAll(s, "\\", "\\\\")
	// Escape double quotes
	escaped = strings.ReplaceAll(escaped, "\"", "\\\"")
	// DO NOT escape $ - we want variable expansion in double quotes
	// DO NOT escape ` - backticks are also allowed in double quotes
	return escaped
}

// isCommandWithStringArg checks if the command is of the form "X -c" where X is a shell/interpreter
// that accepts a command string as argument (e.g., sh -c, bash -c, python -c)
func isCommandWithStringArg(cmd []string) bool {
	if len(cmd) < 3 {
		return false
	}
	// Check if second argument is "-c"
	if cmd[1] != "-c" {
		return false
	}
	// Check if first argument is a known interpreter that accepts -c
	interpreters := map[string]bool{
		"sh":      true,
		"bash":    true,
		"zsh":     true,
		"dash":    true,
		"python":  true,
		"python3": true,
		"python2": true,
		"perl":    true,
		"ruby":    true,
		"node":    true,
		"nodejs":  true,
	}
	return interpreters[cmd[0]]
}

// buildStringArgCommand builds a command with string argument (for -c style commands)
func buildStringArgCommand(cmd []string) string {
	interpreter := cmd[0]
	commandStr := strings.Join(cmd[2:], " ")
	// Escape for double quotes (preserves $ for variable expansion)
	escapedCmd := escapeForDoubleQuotes(commandStr)
	return fmt.Sprintf("%s -c \"%s\"", interpreter, escapedCmd)
}

// ValidateEnvKey validates an environment variable key
func ValidateEnvKey(key string, allowRegex string) bool {
	// Basic validation - should match typical shell variable naming
	if key == "" {
		return false
	}
	// Must start with letter or underscore
	if !(key[0] == '_' || (key[0] >= 'A' && key[0] <= 'Z') || (key[0] >= 'a' && key[0] <= 'z')) {
		return false
	}
	// Rest should be alphanumeric or underscore
	for _, c := range key[1:] {
		if c != '_' && !((c >= 'A' && c <= 'Z') || (c >= 'a' && c <= 'z') || (c >= '0' && c <= '9')) {
			return false
		}
	}
	return true
}
