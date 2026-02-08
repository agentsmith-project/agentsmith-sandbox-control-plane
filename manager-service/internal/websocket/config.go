package websocket

import (
	"errors"
	"net/http"
	"time"

	"github.com/gorilla/websocket"
)

// Config contains WebSocket configuration
type Config struct {
	// ReadBufferSize is the size of the read buffer in bytes
	ReadBufferSize int `yaml:"readBufferSize"`

	// WriteBufferSize is the size of the write buffer in bytes
	WriteBufferSize int `yaml:"writeBufferSize"`

	// AllowedOrigins is a list of allowed WebSocket origins
	AllowedOrigins []string `yaml:"allowedOrigins"`

	// AllowNonBrowserRequests allows requests without Origin header (e.g., CLI tools)
	AllowNonBrowserRequests bool `yaml:"allowNonBrowserRequests"`

	// HandshakeTimeout is the timeout for the WebSocket handshake
	HandshakeTimeout time.Duration `yaml:"handshakeTimeout"`
}

// Validate validates the WebSocket configuration
func (c *Config) Validate() error {
	if c.ReadBufferSize <= 0 {
		return errors.New("readBufferSize must be positive")
	}
	if c.WriteBufferSize <= 0 {
		return errors.New("writeBufferSize must be positive")
	}
	if len(c.AllowedOrigins) == 0 {
		return errors.New("at least one allowed origin is required")
	}
	return nil
}

// Upgrader returns a configured websocket.Upgrader based on this config
func (c *Config) Upgrader() *websocket.Upgrader {
	originSet := make(map[string]bool)
	for _, origin := range c.AllowedOrigins {
		originSet[origin] = true
	}

	return &websocket.Upgrader{
		ReadBufferSize:    c.ReadBufferSize,
		WriteBufferSize:   c.WriteBufferSize,
		HandshakeTimeout:  c.HandshakeTimeout,
		CheckOrigin: func(r *http.Request) bool {
			origin := r.Header.Get("Origin")
			// Allow requests without Origin header (CLI tools, non-browser clients)
			if origin == "" {
				return c.AllowNonBrowserRequests
			}
			// Check if origin is in the allowed set
			return originSet[origin]
		},
	}
}

// DefaultConfig returns the default WebSocket configuration
func DefaultConfig() *Config {
	return &Config{
		ReadBufferSize:          1024,
		WriteBufferSize:         1024,
		AllowedOrigins:          []string{"http://localhost:3000"},
		AllowNonBrowserRequests: true,
		HandshakeTimeout:        10 * time.Second,
	}
}
