# WS Client + README Improvements Implementation Plan

> **For Claude:** REQUIRED SUB-SKILL: Use superpowers:executing-plans to implement this plan task-by-task.

**Goal:** Add TTY resize + reconnect support to the ws-client and update README usage examples.

**Architecture:** Extend the ws-client to capture terminal size changes, send a new `resize` message, and implement simple reconnect with backoff and session reuse. Update README to include Make targets and ws-client examples.

**Tech Stack:** Go, gorilla/websocket

---

### Task 1: Add failing tests for ws-client message helpers

**Files:**
- Create: `manager-service/cmd/ws-client/ws_client_test.go`

**Step 1: Write the failing test**
```go
func TestEncodeResizePayload(t *testing.T) {
    msg, err := buildResizeMessage(120, 40)
    if err != nil { t.Fatal(err) }
    if msg.Type != "resize" { t.Fatalf("type=%s", msg.Type) }
}
```

**Step 2: Run test to verify it fails**
Run: `cd manager-service && go test ./cmd/ws-client -run TestEncodeResizePayload`
Expected: FAIL with undefined buildResizeMessage

**Step 3: Write minimal implementation**
Add helper(s) to build resize message with rows/cols.

**Step 4: Run test to verify it passes**
Run: `cd manager-service && go test ./cmd/ws-client -run TestEncodeResizePayload`
Expected: PASS

**Step 5: Commit**
```bash
git add manager-service/cmd/ws-client/ws_client_test.go manager-service/cmd/ws-client/main.go
git commit -m "test: add ws-client resize message helper"
```

---

### Task 2: Implement terminal resize handling

**Files:**
- Modify: `manager-service/cmd/ws-client/main.go`
- Test: `manager-service/cmd/ws-client/ws_client_test.go`

**Step 1: Write failing test**
```go
func TestResizeStateUpdate(t *testing.T) {
    var st resizeState
    _, ok, _ := st.Update(80, 24)
    if !ok { t.Fatal("expected first update to emit resize") }
    _, ok, _ = st.Update(80, 24)
    if ok { t.Fatal("did not expect resize on same size") }
    _, ok, _ = st.Update(100, 40)
    if !ok { t.Fatal("expected resize on size change") }
}
```

**Step 2: Run test to verify it fails**
Run: `cd manager-service && go test ./cmd/ws-client -run TestResizeMessageJSON`
Expected: FAIL

**Step 3: Write minimal implementation**
Add resizeState helper and wire SIGWINCH -> term.GetSize -> send resize message on change.

**Step 4: Run test to verify it passes**
Run: `cd manager-service && go test ./cmd/ws-client -run TestResizeMessageJSON`
Expected: PASS

**Step 5: Commit**
```bash
git add manager-service/cmd/ws-client

git commit -m "feat: add ws-client resize support"
```

---

### Task 3: Implement reconnect with backoff

**Files:**
- Modify: `manager-service/cmd/ws-client/main.go`
- Test: `manager-service/cmd/ws-client/ws_client_test.go`

**Step 1: Write failing test**
```go
func TestBackoffSequence(t *testing.T) {
    seq := backoffDurations(3)
    if len(seq) != 3 { t.Fatal("bad len") }
}
```

**Step 2: Run test to verify it fails**
Run: `cd manager-service && go test ./cmd/ws-client -run TestBackoffSequence`
Expected: FAIL

**Step 3: Write minimal implementation**
Add reconnect loop with capped exponential backoff; re-send create and continue.

**Step 4: Run test to verify it passes**
Run: `cd manager-service && go test ./cmd/ws-client -run TestBackoffSequence`
Expected: PASS

**Step 5: Commit**
```bash
git add manager-service/cmd/ws-client

git commit -m "feat: add ws-client reconnect"
```

---

### Task 4: Update README with ws-client usage + Make targets

**Files:**
- Modify: `README.md`

**Step 1: Write failing test**
Not applicable (doc change). Confirm in review.

**Step 2: Update README**
Add a short section for Make targets and ws-client usage examples with flags.

**Step 3: Commit**
```bash
git add README.md
git commit -m "docs: add ws-client usage and make targets"
```
