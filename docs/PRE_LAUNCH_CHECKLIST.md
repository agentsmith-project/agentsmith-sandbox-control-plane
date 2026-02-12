# Sandbox Pre-Launch Technical Checklist

**Scope:** Issues that must be fixed before production launch. No scope creep; only items that cause incorrect behavior, stuck resources, or unacceptable security/resource risk.

**Status:** Addressed per 2026-02-12 feedback. Cleaner runs only in `sandbox` namespace; TTL and resource limits clamped to config; finalizer does not store when sessionId missing; smoke tests use pod name from API; cleaner calls Manager DELETE so snapshot is always taken.

---

## 1. ~~Finalizer Only Watches One Namespace~~ → Cleaner Only Cleans `sandbox` (RESOLVED)

**Resolution:** Cleaner only cleans the `sandbox` namespace. `sandbox-system` and `sandbox-workspaces` do not run sandbox pods and are not managed by the cleaner. Removed cleaner CronJobs and RBAC for those namespaces; `allowedNamespaces` in the cleaner binary is only `sandbox`.

---

## 2. ~~Create-Sandbox Request TTL Not Capped~~ (RESOLVED)

**Resolution:** Request `ttlSeconds` is clamped to config max: `sandbox.defaults.ttlSeconds` is the maximum the user can set. In `buildPodSpec`, if `req.TTLSeconds > maxTTL` we set `ttl = maxTTL`.

---

## 3. Create-Sandbox Request Image (ACCEPTED AS-IS)

**Decision:** Any image that is reachable (pullable) is considered trusted. No allowlist; client may pass any image. No code change.

---

## 4. ~~Create-Sandbox Request Resource Limits Not Capped~~ (RESOLVED)

**Resolution:** Config `sandbox.defaults.resources.limits` defines the maximum the user can set. Request CPU/memory/ephemeralStorage are clamped to these limits in `buildPodSpec` (same pattern as TTL).

---

## 5. ~~Finalizer getSandboxID Fallback~~ (RESOLVED)

**Resolution:** When `sandbox/sessionId` annotation is missing, we do not store a snapshot (no fallback to pod name). The finalizer skips snapshot and removes the finalizer so the pod can terminate.

---

## 6. ~~Smoke Tests Use Wrong Pod Name~~ (RESOLVED)

**Resolution:** Create response is parsed for `podName`; it is saved to `/tmp/smoke-test-pod-name.txt` and `SANDBOX_POD_NAME` is used in scenarios. `check_pod_ready` and all smoke steps use `SANDBOX_POD_NAME` when set.

---

## 7. ~~Cleaner-Deleted Pods Must Get Snapshots~~ (RESOLVED)

**Context:** When the **cleaner** CronJob deletes an expired pod, it uses `metav1.DeleteOptions{}` (default grace). The pod has `TerminationGracePeriodSeconds: 1`. The container can be terminated before the Manager’s finalizer loop runs (every 10s). The finalizer then sees the pod with no running containers and skips snapshot, then force-removes the finalizer. So **expired pods cleaned by cron do not get a snapshot**; only client-triggered DELETE gets the synchronous pre-delete snapshot.

**Recommendation:** If product expectation is “expired pods are not snapshotted,” document this clearly. If “every deletion should snapshot when possible,” then either: give the pod a longer grace period when it’s only being deleted by TTL so the finalizer can run, or have the cleaner call the Manager DELETE API instead of deleting the pod directly (so the Manager does the sync snapshot then deletes). This is listed as design clarification; treat as must-fix only if product requires snapshot-on-expiry.

---

## Summary Table

| # | Issue | Status |
|---|--------|--------|
| 1 | Cleaner only cleans `sandbox` namespace | Resolved |
| 2 | Request TTL clamped to config max | Resolved |
| 3 | Image: any reachable image allowed | Accepted |
| 4 | Request resource limits clamped to config max | Resolved |
| 5 | Finalizer: no snapshot when sessionId missing | Resolved |
| 6 | Smoke tests use pod name from create response | Resolved |
| 7 | Cleaner calls Manager DELETE for snapshot before delete | Resolved |
