// sbx-client is a command-line client for the sandbox manager.
package main

import (
	"context"
	"fmt"
	"os"
)

const (
	defaultBaseURL    = "ws://localhost:8080"
	defaultServiceKey = "test-service-key"
)

func main() {
	if len(os.Args) < 2 {
		usage()
		os.Exit(1)
	}

	cmd := os.Args[1]
	baseURL := getEnv("SBX_BASE_URL", defaultBaseURL)
	serviceKey := getEnv("SBX_SERVICE_KEY", defaultServiceKey)

	ctx := context.Background()

	switch cmd {
	case "create":
		handleCreate(ctx, baseURL, serviceKey, os.Args[2:])
	case "attach":
		handleAttach(ctx, baseURL, serviceKey, os.Args[2:])
	case "exec":
		handleExec(ctx, baseURL, serviceKey, os.Args[2:])
	case "close":
		handleClose(ctx, baseURL, serviceKey, os.Args[2:])
	default:
		fmt.Fprintf(os.Stderr, "Unknown command: %s\n", cmd)
		usage()
		os.Exit(1)
	}
}

func getEnv(key, defaultValue string) string {
	if val := os.Getenv(key); val != "" {
		return val
	}
	return defaultValue
}

func usage() {
	fmt.Fprintf(os.Stderr, `Usage: sbx-client <command> [options]

Commands:
  create [--image IMAGE] [--cmd CMD] [--ttl SECONDS]   Create a new session
  attach <session-id>                                  Attach to an existing session
  exec <command>                                       Execute a command in attached session
  close                                                Close the current session

Environment Variables:
  SBX_BASE_URL      Manager service URL (default: ws://localhost:8080)
  SBX_SERVICE_KEY   Service key for authentication (default: test-service-key)

Examples:
  sbx-client create
  sbx-client create --image ubuntu:22.04 --cmd /bin/bash
  sbx-client attach session-abc123
  sbx-client exec "echo hello"
`)
}
