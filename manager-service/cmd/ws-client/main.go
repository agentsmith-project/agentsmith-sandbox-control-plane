package main

import (
	"bufio"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
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

type resizeState struct {
	cols        int
	rows        int
	initialized bool
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

	inputCh := make(chan []byte, 128)
	stdinErrCh := make(chan error, 1)
	go func() {
		stdinErrCh <- stdinLoop(ctx, inputCh, *rawMode, *exitOnCtrl)
	}()

	if err := connectLoop(ctx, *wsURL, create, inputCh); err != nil && !errors.Is(err, context.Canceled) {
		statusf(colorRed, "connection error: %v", err)
	}

	select {
	case err := <-stdinErrCh:
		if err != nil && !errors.Is(err, context.Canceled) {
			statusf(colorRed, "stdin error: %v", err)
		}
	default:
	}
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

func (s *resizeState) Update(cols int, rows int) (Message, bool, error) {
	if cols <= 0 || rows <= 0 {
		return Message{}, false, fmt.Errorf("invalid size cols=%d rows=%d", cols, rows)
	}
	if s.initialized && s.cols == cols && s.rows == rows {
		return Message{}, false, nil
	}
	s.cols = cols
	s.rows = rows
	s.initialized = true
	msg, err := buildResizeMessage(cols, rows)
	if err != nil {
		return Message{}, false, err
	}
	return msg, true, nil
}

func watchResize(ctx context.Context, conn *websocket.Conn) {
	if !term.IsTerminal(int(os.Stdin.Fd())) {
		return
	}
	var st resizeState

	sendCurrent := func() {
		cols, rows, err := term.GetSize(int(os.Stdin.Fd()))
		if err != nil {
			return
		}
		msg, ok, err := st.Update(cols, rows)
		if err != nil || !ok {
			return
		}
		_ = conn.WriteJSON(msg)
	}

	sendCurrent()

	ch := make(chan os.Signal, 1)
	signal.Notify(ch, syscall.SIGWINCH)
	defer signal.Stop(ch)

	for {
		select {
		case <-ctx.Done():
			return
		case <-ch:
			sendCurrent()
		}
	}
}

func readLoop(ctx context.Context, conn *websocket.Conn) error {
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}

		var msg Message
		if err := conn.ReadJSON(&msg); err != nil {
			return err
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
			return io.EOF
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

func stdinLoop(ctx context.Context, inputCh chan<- []byte, rawMode bool, exitKey string) error {
	if !term.IsTerminal(int(os.Stdin.Fd())) {
		rawMode = false
	}

	if rawMode {
		return stdinRaw(ctx, inputCh, exitKey)
	}
	return stdinLine(ctx, inputCh)
}

func stdinLine(ctx context.Context, inputCh chan<- []byte) error {
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
		inputCh <- line
	}
}

func stdinRaw(ctx context.Context, inputCh chan<- []byte, exitKey string) error {
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
				inputCh <- chunk[:idx]
			}
			return context.Canceled
		}
		inputCh <- chunk
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

func connectLoop(ctx context.Context, wsURL string, create CreatePayload, inputCh <-chan []byte) error {
	statusf(colorGreen, "connecting: %s", wsURL)
	statusf(colorBlue, "session: %s", create.AgentThreadID)

	attempt := 0
	for {
		if ctx.Err() != nil {
			return ctx.Err()
		}

		conn, _, err := websocket.DefaultDialer.Dial(wsURL, nil)
		if err != nil {
			wait := backoffDuration(attempt)
			attempt++
			statusf(colorRed, "connect failed, retrying in %s", wait)
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-time.After(wait):
			}
			continue
		}

		attempt = 0
		statusf(colorGreen, "connected")

		if err := sendCreate(conn, create); err != nil {
			_ = conn.Close()
			continue
		}

		connCtx, cancel := context.WithCancel(ctx)
		go watchResize(connCtx, conn)

		readErrCh := make(chan error, 1)
		writeErrCh := make(chan error, 1)

		go func() {
			readErrCh <- readLoop(connCtx, conn)
		}()
		go func() {
			writeErrCh <- writeLoop(connCtx, conn, inputCh)
		}()

		var connErr error
		select {
		case <-ctx.Done():
			cancel()
			_ = conn.Close()
			return ctx.Err()
		case err := <-readErrCh:
			connErr = err
		case err := <-writeErrCh:
			connErr = err
		}

		cancel()
		_ = conn.Close()

		if errors.Is(connErr, context.Canceled) {
			return connErr
		}
		if errors.Is(connErr, io.EOF) {
			return connErr
		}

		wait := backoffDuration(attempt)
		attempt++
		statusf(colorYellow, "disconnected, retrying in %s", wait)
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(wait):
		}
	}
}

func writeLoop(ctx context.Context, conn *websocket.Conn, inputCh <-chan []byte) error {
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case data, ok := <-inputCh:
			if !ok {
				return context.Canceled
			}
			if len(data) == 0 {
				continue
			}
			if err := sendStdin(conn, data); err != nil {
				return err
			}
		}
	}
}

func backoffDurations(n int) []time.Duration {
	if n <= 0 {
		return nil
	}
	seq := make([]time.Duration, 0, n)
	for i := 0; i < n; i++ {
		seq = append(seq, backoffDuration(i))
	}
	return seq
}

func backoffDuration(attempt int) time.Duration {
	base := 250 * time.Millisecond
	max := 5 * time.Second
	wait := base * time.Duration(1<<attempt)
	if wait > max {
		return max
	}
	return wait
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
