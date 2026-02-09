package main

import (
	"flag"
	"log"
	"os"
	"os/exec"

	"github.com/sandbox/shell-bridge/internal/pty"
	"github.com/sandbox/shell-bridge/internal/server"
)

var (
	shellPath = flag.String("shell", "bash", "Shell to spawn (bash, zsh, sh, fish, nu)")
	port      = flag.Int("port", 8080, "WebSocket server port")
	workdir   = flag.String("workdir", "/workspace", "Working directory")
)

func main() {
	flag.Parse()

	// Find shell in PATH
	shell, err := exec.LookPath(*shellPath)
	if err != nil {
		log.Fatalf("Shell not found: %v", err)
	}

	// Change to working directory
	if err := os.Chdir(*workdir); err != nil {
		log.Fatalf("Failed to chdir: %v", err)
	}

	// Create PTY session
	session := pty.NewSession(shell, *workdir)

	// Start WebSocket server
	srv := server.NewServer(*port, session)
	log.Printf("Shell-bridge starting on :%d (shell=%s, workdir=%s)", *port, *shellPath, *workdir)

	if err := srv.Start(); err != nil {
		log.Fatalf("Server error: %v", err)
	}
}
