package auth

import (
	"time"
)

type Permission string

const (
	PermissionCreateSession Permission = "create_session"
	PermissionAccessPod     Permission = "access_pod"
	PermissionExecCommand   Permission = "exec_command"
	PermissionUploadFile    Permission = "upload_file"
	PermissionDownloadFile  Permission = "download_file"
)

type UserContext struct {
	UserID      string
	SessionID   string
	Permissions []Permission
	AuditID     string
	ExpiresAt   time.Time
	CreatedAt   time.Time
}

func (uc *UserContext) IsExpired() bool {
	return time.Now().After(uc.ExpiresAt)
}

func (uc *UserContext) HasPermission(perm Permission) bool {
	for _, p := range uc.Permissions {
		if p == perm {
			return true
		}
	}
	return false
}
