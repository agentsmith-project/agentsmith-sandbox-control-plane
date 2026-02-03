package main

import (
	"bufio"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"net/url"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/gorilla/websocket"
	"golang.org/x/term"
)

type Message struct {
	Type string          `json:"type"`
	Data json.RawMessage `json:"data,omitempty"`
}

type CreatePayload struct {
	AgentThreadID string            `json:"agent_thread_id"`
	Image         string            `json:"image"`
	Command       []string          `json:"command,omitempty"`
	Env           map[string]string `json:"env,omitempty"`
	Config        SecurityConfig    `json:"config"`
}

type SecurityConfig struct {
	AllowNetworkAccess  bool   `json:"allow_network_access"`
	ReadonlyFilesystem  bool   `json:"readonly_filesystem"`
	CPULimit            string `json:"cpu_limit,omitempty"`
	MemoryLimit         string `json:"memory_limit,omitempty"`
	IdleTimeout         string `json:"idle_timeout,omitempty"`
	MaxLifetime         string `json:"max_lifetime,omitempty"`
	DropAllCapabilities bool   `json:"drop_all_capabilities"`
	AllowPrivileged     bool   `json:"allow_privileged"`
}

type StdinPayload struct {
	Data string `json:"data"`
}

type StatusPayload struct {
	State    string  `json:"state"`
	Message  string  `json:"message"`
	Progress float64 `json:"progress"`
}

type ResizePayload struct {
	Cols int `json:"cols"`
	Rows int `json:"rows"`
}

type OutputPayload struct {
	Data string `json:"data"`
}

type ExitPayload struct {
	Code int32 `json:"code"`
}

type ErrorPayload struct {
	Message string `json:"message"`
}

type stringSlice []string

func (s *stringSlice) String() string {
	return strings.Join(*s, ",")
}

func (s *stringSlice) Set(value string) error {
	*s = append(*s, value)
	return nil
}

func main() {
	var (
		wsURL         = flag.String("url", "ws://localhost:8080/ws", "WebSocket URL")
		agentThreadID = flag.String("agent-thread-id", "", "Agent thread/session ID (default: auto)")
		image         = flag.String("image", "python:3.11", "Container image")
		allowNetwork  = flag.Bool("allow-network", false, "Allow outbound network access")
		readonlyFS    = flag.Bool("readonly", false, "Readonly root filesystem")
		cpuLimit      = flag.String("cpu", "", "CPU limit (e.g. 500m, 1)")
		memoryLimit   = flag.String("mem", "", "Memory limit (e.g. 512Mi, 1Gi)")
		idleTimeout   = flag.String("idle", "", "Idle timeout (e.g. 30m)")
		maxLifetime   = flag.String("max", "", "Max lifetime (e.g. 24h)")
		dropCaps      = flag.Bool("drop-caps", false, "Drop all Linux capabilities")
		allowPriv     = flag.Bool("privileged", false, "Allow privileged container")
		rawMode       = flag.Bool("raw", true, "Enable raw TTY mode")
		exitOnCtrl    = flag.String("exit-key", "ctrl-]", "Exit key in raw mode (ctrl-] or ctrl-c)")
	)

	var commands stringSlice
	flag.Var(&commands, "command", "Command to run (repeatable, default: /bin/bash)")

	var envs stringSlice
	flag.Var(&envs, "env", "Environment variable (KEY=VALUE), repeatable")

	flag.Parse()

	if err := validateURL(*wsURL); err != nil {
		fatalf("invalid --url: %v", err)
	}

	if *agentThreadID == "" {
		*agentThreadID = fmt.Sprintf("ws-%d", time.Now().Unix())
	}

	if len(commands) == 0 {
		commands = append(commands, "/bin/bash")
	}

	envMap := map[string]string{}
	for _, kv := range envs {
		key, val, ok := strings.Cut(kv, "=")
		if !ok || key == "" {
			fatalf("invalid --env: %q (expected KEY=VALUE)", kv)
		}
		envMap[key] = val
	}

	create := CreatePayload{
		AgentThreadID: *agentThreadID,
		Image:         *image,
		Command:       commands,
		Env:           envMap,
		Config: SecurityConfig{
			AllowNetworkAccess:  *allowNetwork,
			ReadonlyFilesystem:  *readonlyFS,
			CPULimit:            *cpuLimit,
			MemoryLimit:         *memoryLimit,
			IdleTimeout:         *idleTimeout,
			MaxLifetime:         *maxLifetime,
			DropAllCapabilities: *dropCaps,
			AllowPrivileged:     *allowPriv,
		},
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, os.Interrupt, syscall.SIGTERM)
	go func() {
		<-sigCh
		cancel()
	}()

	dialer := websocket.DefaultDialer
	conn, _, err := dialer.Dial(*wsURL, nil)
	if err != nil {
		fatalf("failed to connect: %v", err)
	}
	defer conn.Close()

	statusf(colorGreen, "connected: %s", *wsURL)
	statusf(colorBlue, "session: %s", create.AgentThreadID)

	if err := sendCreate(conn, create); err != nil {
		fatalf("send create failed: %v", err)
	}

	readDone := make(chan struct{})
	go func() {
		defer close(readDone)
		readLoop(ctx, conn)
	}()

	if err := stdinLoop(ctx, conn, *rawMode, *exitOnCtrl); err != nil {
		if !errors.Is(err, context.Canceled) {
			statusf(colorRed, "stdin error: %v", err)
		}
		cancel()
	}

	<-readDone
}

func validateURL(raw string) error {
	u, err := url.Parse(raw)
	if err != nil {
		return err
	}
	if u.Scheme != "ws" && u.Scheme != "wss" {
		return fmt.Errorf("scheme must be ws or wss")
	}
	return nil
}

func sendCreate(conn *websocket.Conn, payload CreatePayload) error {
	msg := map[string]interface{}{
		"type": "create",
		"data": payload,
	}
	return conn.WriteJSON(msg)
}

func sendStdin(conn *websocket.Conn, data []byte) error {
	msg := map[string]interface{}{
		"type": "stdin",
		"data": StdinPayload{Data: base64.StdEncoding.EncodeToString(data)},
	}
	return conn.WriteJSON(msg)
}

func buildResizeMessage(cols int, rows int) (Message, error) {
	data, err := json.Marshal(ResizePayload{Cols: cols, Rows: rows})
	if err != nil {
		return Message{}, err
	}
	return Message{Type: "resize", Data: data}, nil
}

func readLoop(ctx context.Context, conn *websocket.Conn) {
	for {
		select {
		case <-ctx.Done():
			return
		default:
		}

		var msg Message
		if err := conn.ReadJSON(&msg); err != nil {
			statusf(colorRed, "read error: %v", err)
			return
		}

		switch msg.Type {
		case "status":
			var payload StatusPayload
			if json.Unmarshal(msg.Data, &payload) == nil {
				statusf(colorYellow, "status: %s (%.0f%%) %s", payload.State, payload.Progress*100, payload.Message)
			} else {
				statusf(colorYellow, "status: %s", string(msg.Data))
			}
		case "stdout", "stderr":
			var payload OutputPayload
			if err := json.Unmarshal(msg.Data, &payload); err != nil {
				statusf(colorRed, "invalid output payload: %v", err)
				continue
			}
			data, err := base64.StdEncoding.DecodeString(payload.Data)
			if err != nil {
				statusf(colorRed, "output decode error: %v", err)
				continue
			}
			_, _ = os.Stdout.Write(data)
		case "exit":
			var payload ExitPayload
			if json.Unmarshal(msg.Data, &payload) == nil {
				statusf(colorMagenta, "exit: code=%d", payload.Code)
			} else {
				statusf(colorMagenta, "exit")
			}
			return
		case "error":
			var payload ErrorPayload
			if json.Unmarshal(msg.Data, &payload) == nil {
				statusf(colorRed, "error: %s", payload.Message)
			} else {
				statusf(colorRed, "error: %s", string(msg.Data))
			}
		default:
			statusf(colorBlue, "message: %s", msg.Type)
		}
	}
}

func stdinLoop(ctx context.Context, conn *websocket.Conn, rawMode bool, exitKey string) error {
	if !term.IsTerminal(int(os.Stdin.Fd())) {
		rawMode = false
	}

	if rawMode {
		return stdinRaw(ctx, conn, exitKey)
	}
	return stdinLine(ctx, conn)
}

func stdinLine(ctx context.Context, conn *websocket.Conn) error {
	reader := bufio.NewReader(os.Stdin)
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}

		line, err := reader.ReadBytes('\n')
		if err != nil {
			return err
		}
		if len(line) == 0 {
			continue
		}
		if err := sendStdin(conn, line); err != nil {
			return err
		}
	}
}

func stdinRaw(ctx context.Context, conn *websocket.Conn, exitKey string) error {
	oldState, err := term.MakeRaw(int(os.Stdin.Fd()))
	if err != nil {
		return err
	}
	defer term.Restore(int(os.Stdin.Fd()), oldState)

	exitByte := byte(0x1d) // ctrl-]
	if strings.ToLower(exitKey) == "ctrl-c" {
		exitByte = 0x03
	}

	buf := make([]byte, 4096)
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}

		n, err := os.Stdin.Read(buf)
		if err != nil {
			return err
		}
		if n == 0 {
			continue
		}
		chunk := buf[:n]
		if idx := bytesIndexByte(chunk, exitByte); idx >= 0 {
			if idx > 0 {
				if err := sendStdin(conn, chunk[:idx]); err != nil {
					return err
				}
			}
			return context.Canceled
		}
		if err := sendStdin(conn, chunk); err != nil {
			return err
		}
	}
}

func bytesIndexByte(b []byte, target byte) int {
	for i, v := range b {
		if v == target {
			return i
		}
	}
	return -1
}

const (
	colorReset   = "\033[0m"
	colorRed     = "\033[31m"
	colorGreen   = "\033[32m"
	colorYellow  = "\033[33m"
	colorBlue    = "\033[34m"
	colorMagenta = "\033[35m"
)

func statusf(color string, format string, args ...interface{}) {
	fmt.Fprintf(os.Stderr, "%s%s%s\n", color, fmt.Sprintf(format, args...), colorReset)
}

func fatalf(format string, args ...interface{}) {
	statusf(colorRed, format, args...)
	os.Exit(1)
}
