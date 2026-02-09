package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"log"
	"time"

	"github.com/sandbox/manager/internal/client"
)

func handleCreate(ctx context.Context, baseURL, serviceKey string, args []string) {
	fs := flag.NewFlagSet("create", flag.ExitOnError)
	image := fs.String("image", "sandbox-runner:latest", "Container image")
	cmd := fs.String("cmd", "/bin/bash", "Command to run")
	ttl := fs.Int("ttl", 3600, "Session TTL in seconds")

	if err := fs.Parse(args); err != nil {
		log.Fatal(err)
	}

	c := client.NewClient(baseURL, serviceKey)
	if err := c.Connect(ctx); err != nil {
		log.Fatalf("Connect failed: %v", err)
	}
	defer c.Disconnect()

	req := &client.CreateSessionRequest{
		Image:   *image,
		Command: []string{*cmd},
		Config: client.SecurityConfig{
			IdleTimeout: fmt.Sprintf("%ds", *ttl),
		},
	}

	resp, err := c.CreateSession(ctx, req)
	if err != nil {
		log.Fatalf("Create session failed: %v", err)
	}

	data, _ := json.MarshalIndent(resp, "", "  ")
	fmt.Printf("Session created:\n%s\n", data)

	// For create response, the session ID may be in a message field
	// Show the response structure to user
	fmt.Printf("Response Type: %s\n", resp.Type)
	fmt.Printf("State: %s\n", resp.Data.State)
	if resp.Data.Message != "" {
		fmt.Printf("Message: %s\n", resp.Data.Message)
	}
}

func handleAttach(ctx context.Context, baseURL, serviceKey string, args []string) {
	if len(args) < 1 {
		log.Fatal("Usage: sbx-client attach <session-id>")
	}

	sessionID := args[0]

	c := client.NewClient(baseURL, serviceKey)
	if err := c.Connect(ctx); err != nil {
		log.Fatalf("Connect failed: %v", err)
	}
	defer c.Disconnect()

	// Attach is handled by creating with existing session ID
	req := &client.CreateSessionRequest{
		AgentThreadID: sessionID,
		Image:         "sandbox-runner:latest",
		Command:       []string{"/bin/bash"},
	}

	resp, err := c.CreateSession(ctx, req)
	if err != nil {
		log.Fatalf("Attach failed: %v", err)
	}

	fmt.Printf("Attached to session: %s\n", sessionID)
	fmt.Printf("State: %s\n", resp.Data.State)
	fmt.Println("Reading output (press Ctrl+C to exit)...")

	// Simple output reader - in real implementation, would stream
	ticker := time.NewTicker(5 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-ticker.C:
			fmt.Printf("[%s] Session active\n", time.Now().Format(time.RFC3339))
		case <-ctx.Done():
			return
		}
	}
}

func handleExec(ctx context.Context, baseURL, serviceKey string, args []string) {
	if len(args) < 1 {
		log.Fatal("Usage: sbx-client exec <command>")
	}

	cmd := args[0]

	c := client.NewClient(baseURL, serviceKey)
	if err := c.Connect(ctx); err != nil {
		log.Fatalf("Connect failed: %v", err)
	}
	defer c.Disconnect()

	if err := c.Exec(ctx, cmd); err != nil {
		log.Fatalf("Exec failed: %v", err)
	}

	fmt.Printf("Command sent: %s\n", cmd)
}

func handleClose(ctx context.Context, baseURL, serviceKey string, args []string) {
	c := client.NewClient(baseURL, serviceKey)
	if err := c.Connect(ctx); err != nil {
		log.Fatalf("Connect failed: %v", err)
	}
	defer c.Disconnect()

	if err := c.Close(ctx); err != nil {
		log.Fatalf("Close failed: %v", err)
	}

	fmt.Println("Disconnected")
}
