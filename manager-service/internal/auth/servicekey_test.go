package auth

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
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

func TestParseServiceKeys_EmptyString_ReturnsEmptySlice(t *testing.T) {
	keys := ParseServiceKeys("")
	assert.Empty(t, keys)
}

func TestParseServiceKeys_SingleKey_ReturnsSingleKey(t *testing.T) {
	keys := ParseServiceKeys("key1")
	assert.Len(t, keys, 1)
	assert.Equal(t, "key1", keys[0])
}

func TestParseServiceKeys_MultipleKeys_ReturnsAllKeys(t *testing.T) {
	keys := ParseServiceKeys("key1,key2,key3")
	assert.Len(t, keys, 3)
	assert.Equal(t, "key1", keys[0])
	assert.Equal(t, "key2", keys[1])
	assert.Equal(t, "key3", keys[2])
}

func TestParseServiceKeys_KeysWithWhitespace_TrimsWhitespace(t *testing.T) {
	keys := ParseServiceKeys(" key1 , key2 , key3 ")
	assert.Len(t, keys, 3)
	assert.Equal(t, "key1", keys[0])
	assert.Equal(t, "key2", keys[1])
	assert.Equal(t, "key3", keys[2])
}

func TestParseServiceKeys_EmptyKeysInList_FiltersOutEmptyKeys(t *testing.T) {
	keys := ParseServiceKeys("key1,,key2,,,key3")
	assert.Len(t, keys, 3)
	assert.Equal(t, "key1", keys[0])
	assert.Equal(t, "key2", keys[1])
	assert.Equal(t, "key3", keys[2])
}

func TestParseServiceKeys_WhitespaceOnlyKeys_FiltersOutWhitespaceKeys(t *testing.T) {
	keys := ParseServiceKeys("key1,  ,\t,key2,  ")
	assert.Len(t, keys, 2)
	assert.Equal(t, "key1", keys[0])
	assert.Equal(t, "key2", keys[1])
}

func TestHasKeys_True(t *testing.T) {
	v, err := NewServiceKeyValidator([]string{"key1"})
	require.NoError(t, err)
	assert.True(t, v.HasKeys(), "HasKeys() with one key must be true")
}

func TestHasKeys_WithMultipleKeys_True(t *testing.T) {
	v, err := NewServiceKeyValidator([]string{"a", "b"})
	require.NoError(t, err)
	assert.True(t, v.HasKeys())
}
