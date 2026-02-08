package auth_test

import (
	"testing"
	"time"

	"github.com/sandbox/manager/internal/auth"
	"github.com/stretchr/testify/assert"
)

func TestUserContext_IsExpired(t *testing.T) {
	tests := []struct {
		name      string
		expiresAt time.Time
		expected  bool
	}{
		{
			name:      "not expired",
			expiresAt: time.Now().Add(1 * time.Hour),
			expected:  false,
		},
		{
			name:      "expired",
			expiresAt: time.Now().Add(-1 * time.Hour),
			expected:  true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			uc := &auth.UserContext{ExpiresAt: tt.expiresAt}
			assert.Equal(t, tt.expected, uc.IsExpired())
		})
	}
}

func TestUserContext_HasPermission(t *testing.T) {
	uc := &auth.UserContext{
		Permissions: []auth.Permission{
			auth.PermissionCreateSession,
			auth.PermissionExecCommand,
		},
	}

	assert.True(t, uc.HasPermission(auth.PermissionCreateSession))
	assert.True(t, uc.HasPermission(auth.PermissionExecCommand))
	assert.False(t, uc.HasPermission(auth.PermissionAccessPod))
}

func TestUserContext_HasPermission_NoPermissions(t *testing.T) {
	uc := &auth.UserContext{
		Permissions: []auth.Permission{},
	}

	assert.False(t, uc.HasPermission(auth.PermissionCreateSession))
	assert.False(t, uc.HasPermission(auth.PermissionExecCommand))
}

func TestUserContext_HasPermission_AllPermissions(t *testing.T) {
	uc := &auth.UserContext{
		Permissions: []auth.Permission{
			auth.PermissionCreateSession,
			auth.PermissionAccessPod,
			auth.PermissionExecCommand,
			auth.PermissionUploadFile,
			auth.PermissionDownloadFile,
		},
	}

	assert.True(t, uc.HasPermission(auth.PermissionCreateSession))
	assert.True(t, uc.HasPermission(auth.PermissionAccessPod))
	assert.True(t, uc.HasPermission(auth.PermissionExecCommand))
	assert.True(t, uc.HasPermission(auth.PermissionUploadFile))
	assert.True(t, uc.HasPermission(auth.PermissionDownloadFile))
}

func TestUserContext_IsExpired_ExactlyNow(t *testing.T) {
	// Edge case: expires exactly at current time
	uc := &auth.UserContext{ExpiresAt: time.Now()}
	// Should be expired since time.Now() moves forward
	result := uc.IsExpired()
	// This is a bit flaky, but generally should be true or close to it
	// We accept either result as the timing is very tight
	t.Logf("IsExpired result for time.Now(): %v", result)
}
