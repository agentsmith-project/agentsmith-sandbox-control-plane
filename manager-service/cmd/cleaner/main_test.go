package main

import (
	"testing"
)

// TestSandboxAppLabel verifies that the sandboxAppLabel constant
// matches the label used when creating pods.
func TestSandboxAppLabel(t *testing.T) {
	expected := "llm-sandbox"
	if sandboxAppLabel != expected {
		t.Errorf("sandboxAppLabel = %q, want %q", sandboxAppLabel, expected)
	}
}

// TestAllowedNamespaces verifies that the allowedNamespaces whitelist
// contains the required target namespaces for the cleaner.
func TestAllowedNamespaces(t *testing.T) {
	requiredNamespaces := []string{"sandbox-system", "sandbox-workspaces"}

	// Verify all required namespaces are present
	for _, ns := range requiredNamespaces {
		if !allowedNamespaces[ns] {
			t.Errorf("Namespace %q is not in allowedNamespaces whitelist", ns)
		}
	}

	// Verify no extra namespaces are present
	if len(allowedNamespaces) != len(requiredNamespaces) {
		t.Errorf("allowedNamespaces has %d entries, want %d", len(allowedNamespaces), len(requiredNamespaces))
	}
}
