# Sandbox Pre-Launch Technical Report

**Purpose:** Identify logic gaps, edge cases, and defects that **must** be fixed before production. Scope is limited to issues that cause incorrect behaviour, stuck resources, data loss, or unacceptable security/resource risk.

**Reference:** Existing items in [PRE_LAUNCH_CHECKLIST.md](PRE_LAUNCH_CHECKLIST.md) are treated as resolved. This report adds findings from a full codebase review (2026-02-12).

---

## 1. Upload without Content-Length (chunked) — silent truncation risk

**Location:** `manager-service/internal/httpapi/handlers.go` (HandleUpload), upload flow in `internal/files/tar.go`.

**Issue:**  
For `POST /v1/sandboxes/{id}/files/upload`, the handler only rejects when `r.ContentLength > 0 && r.ContentLength > MaxBytes`. When the client sends **chunked encoding** (no `Content-Length`), the check is skipped and the body is read through `io.LimitReader(r.Body, MaxBytes)`. If the client sends more than `MaxBytes`:

- The server reads only up to `MaxBytes`.
- The tar stream may be truncated; validation may still pass (e.g. valid prefix).
- The server returns **200 OK** while the extracted content in the container is **incomplete or corrupt**.

**Required fix (pick one):**

- **Option A (recommended):** Require `Content-Length` for upload. If `Content-Length` is missing or negative, respond with **411 Length Required** and do not read the body.
- **Option B:** After reading the body, detect when the limit was reached (e.g. wrapper reader that reports “limit hit”) and respond with **413 Payload Too Large** instead of 200 when truncation occurred.

**Priority:** Must-fix before launch (avoids silent data corruption and misleading 200 responses).

---

## 2. Exec request: negative `timeoutSeconds` not rejected

**Location:** `manager-service/internal/httpapi/handlers.go` (HandleExec).

**Issue:**  
`ExecRequest.TimeoutSeconds` is decoded from JSON without validation. The code uses `if req.TimeoutSeconds > 0 { timeout = ... }`, so a negative value is effectively ignored and the config default is used. Semantically, a negative timeout is invalid and should be rejected.

**Required fix:**  
Reject the request when `req.TimeoutSeconds < 0` (e.g. return **400 Bad Request** with a clear message) so the API contract is explicit.

**Priority:** Should-fix before launch (correctness and API clarity; not a security or data-loss issue by itself).

---

## 3. Cleaner: `MANAGER_URL` must be set in production

**Location:** Deployment / CronJob configuration; `cmd/cleaner/main.go` (use of `MANAGER_URL`).

**Issue:**  
When the cleaner runs with `MANAGER_URL` unset or empty, it deletes expired pods **directly via the Kubernetes API**, so **no snapshot** is taken. The design expects the cleaner to call the Manager `DELETE /v1/sandboxes/{sessionId}` so the Manager performs a synchronous snapshot before deleting the pod.

**Required fix:**  
- Ensure production CronJob (e.g. `k8s/base/cleaner-cronjob-sandbox.yaml` or overlay) **always** sets `MANAGER_URL` (e.g. `http://sandbox-manager.sandbox-system.svc.cluster.local`).
- Optionally: have the cleaner **fail fast** or log a clear error when `dry-run=false` and `MANAGER_URL` is empty, so misconfiguration is visible.

**Priority:** Must-fix / must-verify before launch (otherwise expired pods are deleted without snapshot in production).

---

## Summary table

| # | Issue | Type | Priority |
|---|--------|------|----------|
| 1 | Upload without Content-Length can yield 200 with truncated/corrupt data | Logic / data integrity | **Must-fix** |
| 2 | Exec negative `timeoutSeconds` not rejected | Validation | Should-fix |
| 3 | Cleaner must use MANAGER_URL in production so snapshots run | Config / deployment | **Must-verify** |

---

## Out of scope (not required for this launch)

- **DELETE idempotency:** Already correct — `DeletePod` treats NotFound as success, so `DELETE` on a non-existent sandbox returns 204.
- **Pod name length:** `PodName(sessionId)` uses a 10-char hash; total length is well under the Kubernetes 63-character limit.
- **sessionId validation:** HTTP layer (1–128 chars, safe charset) is stricter than storage; no mismatch.
- **Finalizer when `sandbox/sessionId` missing:** Correctly skips snapshot and removes finalizer (no fallback to pod name).
- **Create response after EnsurePod:** If `GetPod` fails after create, the handler returns 5xx; pod exists and a retry would get 200 with existing pod — acceptable.
- **Readiness:** Readyz checks config and K8s only; storage is not part of readiness, which is acceptable as snapshot is best-effort at delete time.
