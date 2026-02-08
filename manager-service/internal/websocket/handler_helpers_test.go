package websocket

import (
	"context"
	"errors"
	"fmt"
	"testing"
)

// TestIsContextCanceled tests the isContextCanceled helper function
func TestIsContextCanceled(t *testing.T) {
	tests := []struct {
		name string
		err  error
		want bool
	}{
		{
			name: "context.Canceled",
			err:  context.Canceled,
			want: true,
		},
		{
			name: "context.DeadlineExceeded",
			err:  context.DeadlineExceeded,
			want: true,
		},
		{
			name: "wrapped context.Canceled",
			err:  fmt.Errorf("operation failed: %w", context.Canceled),
			want: true,
		},
		{
			name: "context canceled error message",
			err:  errors.New("context canceled"),
			want: true,
		},
		{
			name: "generic error",
			err:  errors.New("some other error"),
			want: false,
		},
		{
			name: "nil error",
			err:  nil,
			want: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := isContextCanceled(tt.err); got != tt.want {
				t.Errorf("isContextCanceled() = %v, want %v", got, tt.want)
			}
		})
	}
}

// TestContainsSpace tests the containsSpace helper function
func TestContainsSpace(t *testing.T) {
	tests := []struct {
		name string
		s    string
		want bool
	}{
		{name: "space", s: "hello world", want: true},
		{name: "tab", s: "hello\tworld", want: true},
		{name: "newline", s: "hello\nworld", want: true},
		{name: "carriage return", s: "hello\rworld", want: true},
		{name: "no whitespace", s: "helloworld", want: false},
		{name: "empty string", s: "", want: false},
		{name: "single char without space", s: "a", want: false},
		{name: "single char with space", s: " ", want: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := containsSpace(tt.s); got != tt.want {
				t.Errorf("containsSpace() = %v, want %v", got, tt.want)
			}
		})
	}
}

// TestMarshalJSON tests the marshalJSON helper function
func TestMarshalJSON(t *testing.T) {
	handler := &Handler{}

	tests := []struct {
		name string
		v    interface{}
	}{
		{name: "simple struct", v: struct{ Name string }{Name: "test"}},
		{name: "map", v: map[string]string{"key": "value"}},
		{name: "slice", v: []string{"a", "b", "c"}},
		{name: "nil", v: nil},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := handler.marshalJSON(tt.v)
			if result == nil && tt.v != nil {
				t.Error("marshalJSON() returned nil for non-nil input")
			}
			// Just verify it doesn't panic and returns valid JSON
			if len(result) > 0 && result[0] != '{' && result[0] != '[' && result[0] != 'n' {
				t.Errorf("marshalJSON() returned invalid JSON: %s", string(result))
			}
		})
	}
}
