package k8s

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

// TestIndexOf tests the indexOf helper function
func TestIndexOf(t *testing.T) {
	tests := []struct {
		name      string
		s         string
		substr    string
		idx       int
		expected  int
	}{
		{
			name:     "found at beginning",
			s:        "hello world",
			substr:   "hello",
			idx:      0,
			expected: 0,
		},
		{
			name:     "found in middle",
			s:        "hello world",
			substr:   "world",
			idx:      0,
			expected: 6,
		},
		{
			name:     "found at end",
			s:        "hello",
			substr:   "lo",
			idx:      0,
			expected: 3,
		},
		{
			name:     "not found",
			s:        "hello",
			substr:   "xyz",
			idx:      0,
			expected: -1,
		},
		{
			name:     "empty substring",
			s:        "hello",
			substr:   "",
			idx:      0,
			expected: 0,
		},
		{
			name:     "empty string",
			s:        "",
			substr:   "a",
			idx:      0,
			expected: -1,
		},
		{
			name:     "both empty",
			s:        "",
			substr:   "",
			idx:      0,
			expected: -1, // idx >= len(s) returns -1
		},
		{
			name:     "starting index negative",
			s:        "hello world",
			substr:   "world",
			idx:      -1,
			expected: 6, // Should clamp to 0 and find
		},
		{
			name:     "starting index positive before match",
			s:        "hello world hello",
			substr:   "hello",
			idx:      6,
			expected: 12, // Find second "hello"
		},
		{
			name:     "starting index after match",
			s:        "hello world",
			substr:   "hello",
			idx:      5,
			expected: -1,
		},
		{
			name:     "starting index beyond string length",
			s:        "hello",
			substr:   "h",
			idx:      10,
			expected: -1,
		},
		{
			name:     "starting index at string length",
			s:        "hello",
			substr:   "",
			idx:      5,
			expected: -1, // idx >= len(s) returns -1
		},
		{
			name:     "single char strings match",
			s:        "abc",
			substr:   "b",
			idx:      0,
			expected: 1,
		},
		{
			name:     "multi-char substring",
			s:        "abcdefghij",
			substr:   "def",
			idx:      0,
			expected: 3,
		},
		{
			name:     "substring longer than string",
			s:        "hi",
			substr:   "hello",
			idx:      0,
			expected: -1,
		},
		{
			name:     "special characters",
			s:        "hello@world!",
			substr:   "@",
			idx:      0,
			expected: 5,
		},
		{
			name:     "numbers",
			s:        "123456789",
			substr:   "456",
			idx:      0,
			expected: 3,
		},
		{
			name:     "repeated pattern find first",
			s:        "ababab",
			substr:   "ab",
			idx:      0,
			expected: 0,
		},
		{
			name:     "repeated pattern from index",
			s:        "ababab",
			substr:   "ab",
			idx:      2,
			expected: 2,
		},
		{
			name:     "case sensitive",
			s:        "Hello World",
			substr:   "hello",
			idx:      0,
			expected: -1,
		},
		{
			name:     "unicode characters",
			s:        "hello世界",
			substr:   "世界",
			idx:      0,
			expected: 5,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := indexOf(tt.s, tt.substr, tt.idx)
			assert.Equal(t, tt.expected, result)
		})
	}
}
