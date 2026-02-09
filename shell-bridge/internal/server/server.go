package server

import (
	"bytes"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"os"
	"sync"

	"github.com/gorilla/websocket"
	"github.com/sandbox/shell-bridge/internal/protocol"
	"github.com/sandbox/shell-bridge/internal/pty"
)

var upgrader = websocket.Upgrader{
	ReadBufferSize:  1024,
	WriteBufferSize: 1024,
	CheckOrigin: func(r *http.Request) bool {
		// Basic origin validation - allow from environment variable or localhost
		allowedOrigin := os.Getenv("SHELL_BRIDGE_ALLOWED_ORIGIN")
		if allowedOrigin != "" {
			return r.Header.Get("Origin") == allowedOrigin
		}
		// Default to localhost for development
		origin := r.Header.Get("Origin")
		return origin == "http://localhost" || origin == "http://localhost:*" || origin == ""
	},
}

type Server struct {
	port    int
	session *pty.Session
	mu      sync.Mutex // Protects concurrent access to session
}

type ExecMessage struct {
	Type    string   `json:"type"`
	Shell   string   `json:"shell,omitempty"`
	Command string   `json:"command,omitempty"`
	Env     []string `json:"env,omitempty"`
	Code    int      `json:"code,omitempty"`
	Message string   `json:"message,omitempty"`
}

func NewServer(port int, session *pty.Session) *Server {
	return &Server{
		port:    port,
		session: session,
	}
}

func (s *Server) Start() error {
	if err := s.session.Start(); err != nil {
		return err
	}

	http.HandleFunc("/ws", s.handleWebSocket)
	return http.ListenAndServe(fmt.Sprintf(":%d", s.port), nil)
}

func (s *Server) handleWebSocket(w http.ResponseWriter, r *http.Request) {
	conn, err := upgrader.Upgrade(w, r, nil)
	if err != nil {
		log.Printf("Upgrade failed: %v", err)
		return
	}
	defer func() {
		conn.Close()
		// Ensure session is cleaned up when connection ends
		s.mu.Lock()
		if s.session != nil {
			s.session.Close()
		}
		s.mu.Unlock()
	}()

	// Start output streaming goroutine
	done := make(chan struct{})
	go s.streamOutput(conn, done)

	// Handle input
	for {
		select {
		case <-done:
			return
		default:
			messageType, data, err := conn.ReadMessage()
			if err != nil {
				log.Printf("Read error: %v", err)
				return
			}

			if messageType == websocket.TextMessage {
				var msg ExecMessage
				if err := json.Unmarshal(data, &msg); err != nil {
					log.Printf("JSON error: %v", err)
					continue
				}
				// Handle exec messages if needed
			} else if messageType == websocket.BinaryMessage {
				frame, err := protocol.ParseFrame(bytes.NewReader(data))
				if err != nil {
					log.Printf("Frame parse error: %v", err)
					continue
				}
				if frame.Type == protocol.DataTypeStdout {
					s.mu.Lock()
					s.session.Write(frame.Data)
					s.mu.Unlock()
				}
			}
		}
	}
}

func (s *Server) streamOutput(conn *websocket.Conn, done chan struct{}) {
	buf := make([]byte, 32*1024)
	for {
		select {
		case <-done:
			return
		default:
			s.mu.Lock()
			n, err := s.session.Read(buf)
			s.mu.Unlock()
			if err != nil {
				close(done)
				return
			}

			frame := &protocol.Frame{
				Type:   protocol.DataTypeStdout,
				Length: uint32(n),
				Data:   buf[:n],
			}

			if err := conn.WriteMessage(websocket.BinaryMessage, frame.Bytes()); err != nil {
				log.Printf("Write error: %v", err)
				close(done)
				return
			}
		}
	}
}
