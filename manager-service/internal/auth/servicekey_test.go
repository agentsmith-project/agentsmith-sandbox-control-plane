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

func TestExtractServiceKey_EmptyHeader_ReturnsError(t *testing.T) {
	key, err := ExtractServiceKey("", "Bearer")
	assert.Error(t, err)
	assert.Empty(t, key)
	assert.Contains(t, err.Error(), "empty authorization header")
}

func TestExtractServiceKey_InvalidFormat_NoSpace(t *testing.T) {
	key, err := ExtractServiceKey("Bearer", "Bearer")
	assert.Error(t, err)
	assert.Empty(t, key)
	assert.Contains(t, err.Error(), "invalid authorization header format")
}

func TestExtractServiceKey_ValidBearerToken_ReturnsKey(t *testing.T) {
	key, err := ExtractServiceKey("Bearer my-secret-key", "Bearer")
	assert.NoError(t, err)
	assert.Equal(t, "my-secret-key", key)
}

func TestExtractServiceKey_WrongScheme_ReturnsError(t *testing.T) {
	key, err := ExtractServiceKey("Basic dXNlcjpwYXNz", "Bearer")
	assert.Error(t, err)
	assert.Empty(t, key)
	assert.Contains(t, err.Error(), "unexpected authorization scheme: Basic")
}

func TestExtractServiceKey_KeyWithWhitespace_TrimsWhitespace(t *testing.T) {
	key, err := ExtractServiceKey("Bearer  my-secret-key  ", "Bearer")
	assert.NoError(t, err)
	assert.Equal(t, "my-secret-key", key)
}

func TestExtractServiceKey_EmptyKeyAfterScheme_ReturnsError(t *testing.T) {
	key, err := ExtractServiceKey("Bearer   ", "Bearer")
	assert.Error(t, err)
	assert.Empty(t, key)
	assert.Contains(t, err.Error(), "empty key in authorization header")
}

func TestExtractServiceKey_CustomScheme_WorksCorrectly(t *testing.T) {
	key, err := ExtractServiceKey("CustomScheme my-key", "CustomScheme")
	assert.NoError(t, err)
	assert.Equal(t, "my-key", key)
}
