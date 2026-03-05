package main

import (
	"testing"
)

// TestSandboxAppLabel verifies that the sandboxAppLabel constant
// matches the label used when creating pods.
func TestSandboxAppLabel(t *testing.T) {
	expected := "sandbox"
	if sandboxAppLabel != expected {
		t.Errorf("sandboxAppLabel = %q, want %q", sandboxAppLabel, expected)
	}
}
