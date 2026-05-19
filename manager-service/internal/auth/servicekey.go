package auth

import (
	"crypto/subtle"
	"fmt"
	"strings"
)

// ServiceKeyValidator validates service keys
type ServiceKeyValidator struct {
	validKeys map[string]struct{}
}

// NewServiceKeyValidator creates a new service key validator
// Returns an error if no valid keys are provided
func NewServiceKeyValidator(keys []string) (*ServiceKeyValidator, error) {
	validKeys := make(map[string]struct{})
	for _, key := range keys {
		trimmed := strings.TrimSpace(key)
		if trimmed != "" {
			validKeys[trimmed] = struct{}{}
		}
	}

	if len(validKeys) == 0 {
		return nil, fmt.Errorf("ASBCP_SERVICE_KEYS cannot be empty: service requires authentication")
	}

	return &ServiceKeyValidator{
		validKeys: validKeys,
	}, nil
}

// Validate checks if a key is valid using constant-time comparison
func (v *ServiceKeyValidator) Validate(key string) bool {
	if key == "" {
		return false
	}

	// Check against all valid keys using constant-time comparison
	for validKey := range v.validKeys {
		if ConstantTimeCompare(key, validKey) {
			return true
		}
	}
	return false
}

// HasKeys returns true if there are any valid keys configured
func (v *ServiceKeyValidator) HasKeys() bool {
	return len(v.validKeys) > 0
}

// Count returns the number of valid keys
func (v *ServiceKeyValidator) Count() int {
	return len(v.validKeys)
}

// ConstantTimeCompare compares two strings in constant time
// This prevents timing attacks that could be used to guess valid keys
func ConstantTimeCompare(a, b string) bool {
	if subtle.ConstantTimeCompare([]byte(a), []byte(b)) == 1 {
		return true
	}
	return false
}

// ParseServiceKeys parses service keys from a comma-separated string
func ParseServiceKeys(keysStr string) []string {
	if keysStr == "" {
		return []string{}
	}

	parts := strings.Split(keysStr, ",")
	keys := make([]string, 0, len(parts))

	for _, part := range parts {
		key := strings.TrimSpace(part)
		if key != "" {
			keys = append(keys, key)
		}
	}

	return keys
}
