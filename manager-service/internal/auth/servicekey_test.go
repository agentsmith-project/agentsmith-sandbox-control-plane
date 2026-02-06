package auth

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestNewServiceKeyValidator_EmptyKeys_ReturnsError(t *testing.T) {
	_, err := NewServiceKeyValidator([]string{})
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "SERVICE_KEYS cannot be empty")
}

func TestNewServiceKeyValidator_WhitespaceOnlyKeys_ReturnsError(t *testing.T) {
	_, err := NewServiceKeyValidator([]string{"  ", "\t", "  "})
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "SERVICE_KEYS cannot be empty")
}

func TestNewServiceKeyValidator_ValidKeys_ReturnsValidator(t *testing.T) {
	validator, err := NewServiceKeyValidator([]string{"key1", "key2"})
	assert.NoError(t, err)
	assert.NotNil(t, validator)
	assert.Equal(t, 2, validator.Count())
}

func TestNewServiceKeyValidator_MixedValidAndInvalidKeys_TrimsAndFilters(t *testing.T) {
	validator, err := NewServiceKeyValidator([]string{"  key1  ", "key2", "  ", "\t", "key3"})
	assert.NoError(t, err)
	assert.NotNil(t, validator)
	assert.Equal(t, 3, validator.Count())
}
