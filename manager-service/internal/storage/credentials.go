package storage

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// Credentials contains storage access credentials
type Credentials struct {
	AccessKey string
	SecretKey string
	Endpoint  string
	Bucket    string
	UseSSL    bool
}

// LoadCredentials loads storage credentials from either a credentials file
// or environment variables (fallback for backward compatibility).
//
// It first checks STORAGE_CREDENTIALS_PATH environment variable.
// If set, it reads credentials from files in that directory:
//   - <path>/access.key for access key
//   - <path>/secret.key for secret key
//
// If STORAGE_CREDENTIALS_PATH is not set, it falls back to environment variables:
//   - STORAGE_ACCESS_KEY
//   - STORAGE_SECRET_KEY
//   - STORAGE_ENDPOINT
//   - STORAGE_BUCKET
//   - STORAGE_USE_SSL
func LoadCredentials() (*Credentials, error) {
	// Try loading from file first
	if credPath := os.Getenv("STORAGE_CREDENTIALS_PATH"); credPath != "" {
		return loadFromFile(credPath)
	}

	// Fall back to environment variables
	return loadFromEnv()
}

// loadFromFile loads credentials from a directory containing access.key and secret.key files
func loadFromFile(path string) (*Credentials, error) {
	accessKeyPath := filepath.Join(path, "access.key")
	secretKeyPath := filepath.Join(path, "secret.key")

	accessKeyBytes, err := os.ReadFile(accessKeyPath)
	if err != nil {
		return nil, fmt.Errorf("failed to read access key from %s: %w", accessKeyPath, err)
	}

	secretKeyBytes, err := os.ReadFile(secretKeyPath)
	if err != nil {
		return nil, fmt.Errorf("failed to read secret key from %s: %w", secretKeyPath, err)
	}

	return &Credentials{
		AccessKey: strings.TrimSpace(string(accessKeyBytes)),
		SecretKey: strings.TrimSpace(string(secretKeyBytes)),
		Endpoint:  os.Getenv("STORAGE_ENDPOINT"),
		Bucket:    os.Getenv("STORAGE_BUCKET"),
		UseSSL:    os.Getenv("STORAGE_USE_SSL") == "true",
	}, nil
}

// loadFromEnv loads credentials from environment variables
func loadFromEnv() (*Credentials, error) {
	accessKey := os.Getenv("STORAGE_ACCESS_KEY")
	if accessKey == "" {
		return nil, fmt.Errorf("STORAGE_ACCESS_KEY environment variable is required")
	}

	secretKey := os.Getenv("STORAGE_SECRET_KEY")
	if secretKey == "" {
		return nil, fmt.Errorf("STORAGE_SECRET_KEY environment variable is required")
	}

	endpoint := os.Getenv("STORAGE_ENDPOINT")
	if endpoint == "" {
		return nil, fmt.Errorf("STORAGE_ENDPOINT environment variable is required")
	}

	bucket := os.Getenv("STORAGE_BUCKET")
	if bucket == "" {
		return nil, fmt.Errorf("STORAGE_BUCKET environment variable is required")
	}

	return &Credentials{
		AccessKey: accessKey,
		SecretKey: secretKey,
		Endpoint:  endpoint,
		Bucket:    bucket,
		UseSSL:    os.Getenv("STORAGE_USE_SSL") == "true",
	}, nil
}

// Validate checks if the credentials are valid for use
func (c *Credentials) Validate() error {
	if c.AccessKey == "" {
		return fmt.Errorf("access key cannot be empty")
	}
	if c.SecretKey == "" {
		return fmt.Errorf("secret key cannot be empty")
	}
	if c.Endpoint == "" {
		return fmt.Errorf("endpoint cannot be empty")
	}
	if c.Bucket == "" {
		return fmt.Errorf("bucket cannot be empty")
	}
	return nil
}
