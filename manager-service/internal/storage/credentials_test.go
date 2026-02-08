package storage

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestLoadCredentials(t *testing.T) {
	// Save original environment
	origEnv := map[string]string{
		"STORAGE_CREDENTIALS_PATH": "",
		"STORAGE_ACCESS_KEY":       "",
		"STORAGE_SECRET_KEY":       "",
		"STORAGE_ENDPOINT":         "",
		"STORAGE_BUCKET":           "",
		"STORAGE_USE_SSL":          "",
	}
	for k := range origEnv {
		origEnv[k] = os.Getenv(k)
	}

	// Clean up environment after test
	defer func() {
		for k, v := range origEnv {
			if v == "" {
				os.Unsetenv(k)
			} else {
				os.Setenv(k, v)
			}
		}
	}()

	t.Run("loads from file when STORAGE_CREDENTIALS_PATH is set", func(t *testing.T) {
		tmpDir := t.TempDir()
		credPath := filepath.Join(tmpDir, "creds")

		// Create credential files
		if err := os.Mkdir(credPath, 0755); err != nil {
			t.Fatalf("Failed to create cred dir: %v", err)
		}
		if err := os.WriteFile(filepath.Join(credPath, "access.key"), []byte("test-access-key"), 0600); err != nil {
			t.Fatalf("Failed to write access.key: %v", err)
		}
		if err := os.WriteFile(filepath.Join(credPath, "secret.key"), []byte("test-secret-key"), 0600); err != nil {
			t.Fatalf("Failed to write secret.key: %v", err)
		}

		// Set environment variables for endpoint and bucket
		os.Setenv("STORAGE_CREDENTIALS_PATH", credPath)
		os.Setenv("STORAGE_ENDPOINT", "http://localhost:9000")
		os.Setenv("STORAGE_BUCKET", "test-bucket")
		os.Unsetenv("STORAGE_ACCESS_KEY")
		os.Unsetenv("STORAGE_SECRET_KEY")

		creds, err := LoadCredentials()
		if err != nil {
			t.Fatalf("LoadCredentials() error = %v", err)
		}

		if creds.AccessKey != "test-access-key" {
			t.Errorf("LoadCredentials() accessKey = %v, want 'test-access-key'", creds.AccessKey)
		}
		if creds.SecretKey != "test-secret-key" {
			t.Errorf("LoadCredentials() secretKey = %v, want 'test-secret-key'", creds.SecretKey)
		}
		if creds.Endpoint != "http://localhost:9000" {
			t.Errorf("LoadCredentials() endpoint = %v, want 'http://localhost:9000'", creds.Endpoint)
		}
		if creds.Bucket != "test-bucket" {
			t.Errorf("LoadCredentials() bucket = %v, want 'test-bucket'", creds.Bucket)
		}
		if creds.UseSSL {
			t.Error("LoadCredentials() useSSL = true, want false")
		}
	})

	t.Run("trims whitespace from file credentials", func(t *testing.T) {
		tmpDir := t.TempDir()
		credPath := filepath.Join(tmpDir, "creds")

		if err := os.Mkdir(credPath, 0755); err != nil {
			t.Fatalf("Failed to create cred dir: %v", err)
		}
		// Write credentials with trailing newlines
		if err := os.WriteFile(filepath.Join(credPath, "access.key"), []byte("test-access-key\n"), 0600); err != nil {
			t.Fatalf("Failed to write access.key: %v", err)
		}
		if err := os.WriteFile(filepath.Join(credPath, "secret.key"), []byte("test-secret-key  \n"), 0600); err != nil {
			t.Fatalf("Failed to write secret.key: %v", err)
		}

		os.Setenv("STORAGE_CREDENTIALS_PATH", credPath)
		os.Setenv("STORAGE_ENDPOINT", "http://localhost:9000")
		os.Setenv("STORAGE_BUCKET", "test-bucket")

		creds, err := LoadCredentials()
		if err != nil {
			t.Fatalf("LoadCredentials() error = %v", err)
		}

		if creds.AccessKey != "test-access-key" {
			t.Errorf("LoadCredentials() accessKey = %v, want 'test-access-key' (trimmed)", creds.AccessKey)
		}
		if creds.SecretKey != "test-secret-key" {
			t.Errorf("LoadCredentials() secretKey = %v, want 'test-secret-key' (trimmed)", creds.SecretKey)
		}
	})

	t.Run("loads from environment when STORAGE_CREDENTIALS_PATH is not set", func(t *testing.T) {
		os.Unsetenv("STORAGE_CREDENTIALS_PATH")
		os.Setenv("STORAGE_ACCESS_KEY", "env-access-key")
		os.Setenv("STORAGE_SECRET_KEY", "env-secret-key")
		os.Setenv("STORAGE_ENDPOINT", "http://env-endpoint:9000")
		os.Setenv("STORAGE_BUCKET", "env-bucket")

		creds, err := LoadCredentials()
		if err != nil {
			t.Fatalf("LoadCredentials() error = %v", err)
		}

		if creds.AccessKey != "env-access-key" {
			t.Errorf("LoadCredentials() accessKey = %v, want 'env-access-key'", creds.AccessKey)
		}
		if creds.SecretKey != "env-secret-key" {
			t.Errorf("LoadCredentials() secretKey = %v, want 'env-secret-key'", creds.SecretKey)
		}
		if creds.Endpoint != "http://env-endpoint:9000" {
			t.Errorf("LoadCredentials() endpoint = %v, want 'http://env-endpoint:9000'", creds.Endpoint)
		}
		if creds.Bucket != "env-bucket" {
			t.Errorf("LoadCredentials() bucket = %v, want 'env-bucket'", creds.Bucket)
		}
	})

	t.Run("parses STORAGE_USE_SSL from environment", func(t *testing.T) {
		os.Unsetenv("STORAGE_CREDENTIALS_PATH")
		os.Setenv("STORAGE_ACCESS_KEY", "access")
		os.Setenv("STORAGE_SECRET_KEY", "secret")
		os.Setenv("STORAGE_ENDPOINT", "http://localhost:9000")
		os.Setenv("STORAGE_BUCKET", "bucket")
		os.Setenv("STORAGE_USE_SSL", "true")

		creds, err := LoadCredentials()
		if err != nil {
			t.Fatalf("LoadCredentials() error = %v", err)
		}

		if !creds.UseSSL {
			t.Error("LoadCredentials() useSSL = false, want true")
		}

		// Test with false
		os.Setenv("STORAGE_USE_SSL", "false")
		creds, err = LoadCredentials()
		if err != nil {
			t.Fatalf("LoadCredentials() error = %v", err)
		}

		if creds.UseSSL {
			t.Error("LoadCredentials() useSSL = true, want false")
		}

		// Test with invalid value
		os.Setenv("STORAGE_USE_SSL", "invalid")
		creds, err = LoadCredentials()
		if err != nil {
			t.Fatalf("LoadCredentials() error = %v", err)
		}

		if creds.UseSSL {
			t.Error("LoadCredentials() useSSL = true, want false for invalid value")
		}
	})

	t.Run("returns error when file access key missing", func(t *testing.T) {
		tmpDir := t.TempDir()
		credPath := filepath.Join(tmpDir, "creds")

		if err := os.Mkdir(credPath, 0755); err != nil {
			t.Fatalf("Failed to create cred dir: %v", err)
		}
		// Only write secret.key, not access.key
		if err := os.WriteFile(filepath.Join(credPath, "secret.key"), []byte("secret"), 0600); err != nil {
			t.Fatalf("Failed to write secret.key: %v", err)
		}

		os.Setenv("STORAGE_CREDENTIALS_PATH", credPath)
		os.Setenv("STORAGE_ENDPOINT", "http://localhost:9000")
		os.Setenv("STORAGE_BUCKET", "bucket")

		_, err := LoadCredentials()
		if err == nil {
			t.Error("LoadCredentials() expected error, got nil")
		}
		if !strings.Contains(err.Error(), "access key") {
			t.Errorf("LoadCredentials() error = %v, want 'access key' in error", err)
		}
	})

	t.Run("returns error when file secret key missing", func(t *testing.T) {
		tmpDir := t.TempDir()
		credPath := filepath.Join(tmpDir, "creds")

		if err := os.Mkdir(credPath, 0755); err != nil {
			t.Fatalf("Failed to create cred dir: %v", err)
		}
		// Only write access.key, not secret.key
		if err := os.WriteFile(filepath.Join(credPath, "access.key"), []byte("access"), 0600); err != nil {
			t.Fatalf("Failed to write access.key: %v", err)
		}

		os.Setenv("STORAGE_CREDENTIALS_PATH", credPath)
		os.Setenv("STORAGE_ENDPOINT", "http://localhost:9000")
		os.Setenv("STORAGE_BUCKET", "bucket")

		_, err := LoadCredentials()
		if err == nil {
			t.Error("LoadCredentials() expected error, got nil")
		}
		if !strings.Contains(err.Error(), "secret key") {
			t.Errorf("LoadCredentials() error = %v, want 'secret key' in error", err)
		}
	})

	t.Run("returns error when env access key missing", func(t *testing.T) {
		os.Unsetenv("STORAGE_CREDENTIALS_PATH")
		os.Unsetenv("STORAGE_ACCESS_KEY")
		os.Setenv("STORAGE_SECRET_KEY", "secret")
		os.Setenv("STORAGE_ENDPOINT", "http://localhost:9000")
		os.Setenv("STORAGE_BUCKET", "bucket")

		_, err := LoadCredentials()
		if err == nil {
			t.Error("LoadCredentials() expected error, got nil")
		}
		if !strings.Contains(err.Error(), "STORAGE_ACCESS_KEY") {
			t.Errorf("LoadCredentials() error = %v, want 'STORAGE_ACCESS_KEY' in error", err)
		}
	})

	t.Run("returns error when env secret key missing", func(t *testing.T) {
		os.Unsetenv("STORAGE_CREDENTIALS_PATH")
		os.Setenv("STORAGE_ACCESS_KEY", "access")
		os.Unsetenv("STORAGE_SECRET_KEY")
		os.Setenv("STORAGE_ENDPOINT", "http://localhost:9000")
		os.Setenv("STORAGE_BUCKET", "bucket")

		_, err := LoadCredentials()
		if err == nil {
			t.Error("LoadCredentials() expected error, got nil")
		}
		if !strings.Contains(err.Error(), "STORAGE_SECRET_KEY") {
			t.Errorf("LoadCredentials() error = %v, want 'STORAGE_SECRET_KEY' in error", err)
		}
	})

	t.Run("returns error when env endpoint missing", func(t *testing.T) {
		os.Unsetenv("STORAGE_CREDENTIALS_PATH")
		os.Setenv("STORAGE_ACCESS_KEY", "access")
		os.Setenv("STORAGE_SECRET_KEY", "secret")
		os.Unsetenv("STORAGE_ENDPOINT")
		os.Setenv("STORAGE_BUCKET", "bucket")

		_, err := LoadCredentials()
		if err == nil {
			t.Error("LoadCredentials() expected error, got nil")
		}
		if !strings.Contains(err.Error(), "STORAGE_ENDPOINT") {
			t.Errorf("LoadCredentials() error = %v, want 'STORAGE_ENDPOINT' in error", err)
		}
	})

	t.Run("returns error when env bucket missing", func(t *testing.T) {
		os.Unsetenv("STORAGE_CREDENTIALS_PATH")
		os.Setenv("STORAGE_ACCESS_KEY", "access")
		os.Setenv("STORAGE_SECRET_KEY", "secret")
		os.Setenv("STORAGE_ENDPOINT", "http://localhost:9000")
		os.Unsetenv("STORAGE_BUCKET")

		_, err := LoadCredentials()
		if err == nil {
			t.Error("LoadCredentials() expected error, got nil")
		}
		if !strings.Contains(err.Error(), "STORAGE_BUCKET") {
			t.Errorf("LoadCredentials() error = %v, want 'STORAGE_BUCKET' in error", err)
		}
	})
}

func TestLoadFromFile(t *testing.T) {
	t.Run("successful load from file", func(t *testing.T) {
		tmpDir := t.TempDir()
		credPath := filepath.Join(tmpDir, "creds")

		if err := os.Mkdir(credPath, 0755); err != nil {
			t.Fatalf("Failed to create cred dir: %v", err)
		}
		if err := os.WriteFile(filepath.Join(credPath, "access.key"), []byte("my-access"), 0600); err != nil {
			t.Fatalf("Failed to write access.key: %v", err)
		}
		if err := os.WriteFile(filepath.Join(credPath, "secret.key"), []byte("my-secret"), 0600); err != nil {
			t.Fatalf("Failed to write secret.key: %v", err)
		}

		os.Setenv("STORAGE_ENDPOINT", "http://localhost:9000")
		os.Setenv("STORAGE_BUCKET", "my-bucket")
		os.Setenv("STORAGE_USE_SSL", "true")

		creds, err := loadFromFile(credPath)
		if err != nil {
			t.Fatalf("loadFromFile() error = %v", err)
		}

		if creds.AccessKey != "my-access" {
			t.Errorf("loadFromFile() accessKey = %v, want 'my-access'", creds.AccessKey)
		}
		if creds.SecretKey != "my-secret" {
			t.Errorf("loadFromFile() secretKey = %v, want 'my-secret'", creds.SecretKey)
		}
		if creds.Endpoint != "http://localhost:9000" {
			t.Errorf("loadFromFile() endpoint = %v, want 'http://localhost:9000'", creds.Endpoint)
		}
		if creds.Bucket != "my-bucket" {
			t.Errorf("loadFromFile() bucket = %v, want 'my-bucket'", creds.Bucket)
		}
		if !creds.UseSSL {
			t.Error("loadFromFile() useSSL = false, want true")
		}
	})

	t.Run("uses empty string for endpoint and bucket when not set", func(t *testing.T) {
		tmpDir := t.TempDir()
		credPath := filepath.Join(tmpDir, "creds")

		if err := os.Mkdir(credPath, 0755); err != nil {
			t.Fatalf("Failed to create cred dir: %v", err)
		}
		if err := os.WriteFile(filepath.Join(credPath, "access.key"), []byte("access"), 0600); err != nil {
			t.Fatalf("Failed to write access.key: %v", err)
		}
		if err := os.WriteFile(filepath.Join(credPath, "secret.key"), []byte("secret"), 0600); err != nil {
			t.Fatalf("Failed to write secret.key: %v", err)
		}

		os.Unsetenv("STORAGE_ENDPOINT")
		os.Unsetenv("STORAGE_BUCKET")

		creds, err := loadFromFile(credPath)
		if err != nil {
			t.Fatalf("loadFromFile() error = %v", err)
		}

		if creds.Endpoint != "" {
			t.Errorf("loadFromFile() endpoint = %v, want ''", creds.Endpoint)
		}
		if creds.Bucket != "" {
			t.Errorf("loadFromFile() bucket = %v, want ''", creds.Bucket)
		}
	})
}

func TestLoadFromEnv(t *testing.T) {
	// Save original environment
	origEnv := map[string]string{
		"STORAGE_ACCESS_KEY": "",
		"STORAGE_SECRET_KEY": "",
		"STORAGE_ENDPOINT":   "",
		"STORAGE_BUCKET":     "",
		"STORAGE_USE_SSL":    "",
	}
	for k := range origEnv {
		origEnv[k] = os.Getenv(k)
	}

	// Clean up environment after test
	defer func() {
		for k, v := range origEnv {
			if v == "" {
				os.Unsetenv(k)
			} else {
				os.Setenv(k, v)
			}
		}
	}()

	t.Run("successful load from env", func(t *testing.T) {
		os.Setenv("STORAGE_ACCESS_KEY", "env-access")
		os.Setenv("STORAGE_SECRET_KEY", "env-secret")
		os.Setenv("STORAGE_ENDPOINT", "http://env-endpoint:9000")
		os.Setenv("STORAGE_BUCKET", "env-bucket")
		os.Setenv("STORAGE_USE_SSL", "false")

		creds, err := loadFromEnv()
		if err != nil {
			t.Fatalf("loadFromEnv() error = %v", err)
		}

		if creds.AccessKey != "env-access" {
			t.Errorf("loadFromEnv() accessKey = %v, want 'env-access'", creds.AccessKey)
		}
		if creds.SecretKey != "env-secret" {
			t.Errorf("loadFromEnv() secretKey = %v, want 'env-secret'", creds.SecretKey)
		}
		if creds.Endpoint != "http://env-endpoint:9000" {
			t.Errorf("loadFromEnv() endpoint = %v, want 'http://env-endpoint:9000'", creds.Endpoint)
		}
		if creds.Bucket != "env-bucket" {
			t.Errorf("loadFromEnv() bucket = %v, want 'env-bucket'", creds.Bucket)
		}
		if creds.UseSSL {
			t.Error("loadFromEnv() useSSL = true, want false")
		}
	})

	t.Run("returns error for missing access key", func(t *testing.T) {
		os.Unsetenv("STORAGE_ACCESS_KEY")
		os.Setenv("STORAGE_SECRET_KEY", "secret")
		os.Setenv("STORAGE_ENDPOINT", "http://localhost:9000")
		os.Setenv("STORAGE_BUCKET", "bucket")

		_, err := loadFromEnv()
		if err == nil {
			t.Error("loadFromEnv() expected error, got nil")
		}
	})

	t.Run("returns error for missing secret key", func(t *testing.T) {
		os.Setenv("STORAGE_ACCESS_KEY", "access")
		os.Unsetenv("STORAGE_SECRET_KEY")
		os.Setenv("STORAGE_ENDPOINT", "http://localhost:9000")
		os.Setenv("STORAGE_BUCKET", "bucket")

		_, err := loadFromEnv()
		if err == nil {
			t.Error("loadFromEnv() expected error, got nil")
		}
	})

	t.Run("returns error for missing endpoint", func(t *testing.T) {
		os.Setenv("STORAGE_ACCESS_KEY", "access")
		os.Setenv("STORAGE_SECRET_KEY", "secret")
		os.Unsetenv("STORAGE_ENDPOINT")
		os.Setenv("STORAGE_BUCKET", "bucket")

		_, err := loadFromEnv()
		if err == nil {
			t.Error("loadFromEnv() expected error, got nil")
		}
	})

	t.Run("returns error for missing bucket", func(t *testing.T) {
		os.Setenv("STORAGE_ACCESS_KEY", "access")
		os.Setenv("STORAGE_SECRET_KEY", "secret")
		os.Setenv("STORAGE_ENDPOINT", "http://localhost:9000")
		os.Unsetenv("STORAGE_BUCKET")

		_, err := loadFromEnv()
		if err == nil {
			t.Error("loadFromEnv() expected error, got nil")
		}
	})
}

func TestCredentials_Validate(t *testing.T) {
	tests := []struct {
		name    string
		creds   *Credentials
		wantErr bool
		errMsg  string
	}{
		{
			name: "valid credentials",
			creds: &Credentials{
				AccessKey: "test-access",
				SecretKey: "test-secret",
				Endpoint:  "http://localhost:9000",
				Bucket:    "test-bucket",
				UseSSL:    false,
			},
			wantErr: false,
		},
		{
			name: "empty access key",
			creds: &Credentials{
				AccessKey: "",
				SecretKey: "secret",
				Endpoint:  "http://localhost:9000",
				Bucket:    "bucket",
			},
			wantErr: true,
			errMsg:  "access key",
		},
		{
			name: "empty secret key",
			creds: &Credentials{
				AccessKey: "access",
				SecretKey: "",
				Endpoint:  "http://localhost:9000",
				Bucket:    "bucket",
			},
			wantErr: true,
			errMsg:  "secret key",
		},
		{
			name: "empty endpoint",
			creds: &Credentials{
				AccessKey: "access",
				SecretKey: "secret",
				Endpoint:  "",
				Bucket:    "bucket",
			},
			wantErr: true,
			errMsg:  "endpoint",
		},
		{
			name: "empty bucket",
			creds: &Credentials{
				AccessKey: "access",
				SecretKey: "secret",
				Endpoint:  "http://localhost:9000",
				Bucket:    "",
			},
			wantErr: true,
			errMsg:  "bucket",
		},
		{
			name: "all empty",
			creds: &Credentials{
				AccessKey: "",
				SecretKey: "",
				Endpoint:  "",
				Bucket:    "",
			},
			wantErr: true,
			// Will fail on access key check first
			errMsg: "access key",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := tt.creds.Validate()
			if (err != nil) != tt.wantErr {
				t.Errorf("Credentials.Validate() error = %v, wantErr %v", err, tt.wantErr)
				return
			}
			if err != nil && tt.errMsg != "" {
				if !strings.Contains(strings.ToLower(err.Error()), tt.errMsg) {
					t.Errorf("Credentials.Validate() error = %v, want to contain %v", err, tt.errMsg)
				}
			}
		})
	}
}
