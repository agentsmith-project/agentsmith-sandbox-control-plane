# Integration Testing Guide

**Status:** ⚠️ Pending (Dependencies to resolve)

## Summary

This document describes how to perform integration testing of the enterprise-grade manager-service.

## Prerequisites

### Cluster Setup

Ensure the kind cluster is running and runner/gc are deployed:

```bash
# Check cluster
kubectl cluster-info --context kind-sandbox

# Check existing pods
kubectl get pods -n sandbox-system
kubectl get pods -n sandbox
```

### Dependencies to Resolve

The following Go dependencies need to be added (network issue encountered):

```bash
go get github.com/fsnotify/fsnotify@v1.6.0
go get github.com/google/uuid@v1.3.0
go get gopkg.in/yaml.v3@v3.0.1
```

## Local Testing (Cluster External)

### Step 1: Prepare Environment

```bash
cd manager-service

# Create a test config file
cat > /tmp/manager-config.yaml <<'EOF'
version: 1

server:
  httpPort: 8080
  requestIdHeader: X-Request-Id
  timeouts:
    readHeader: 5s
    read: 30s
    write: 60s
    idle: 120s
  maxHeaderBytes: 1048576
  metrics:
    enabled: true
    path: /metrics
    requireServiceKey: false
  debug:
    configPath: /debug/config
    enablePprof: false

auth:
  enabled: true
  headerName: X-Service-Key
  acceptAuthorization: true
  authorizationScheme: ServiceKey
  failStatusCode: 401

kubernetes:
  qps: 50
  burst: 100
  requestTimeout: 15s
  retry:
    enabled: true
    maxAttempts: 3
    baseBackoff: 200ms
    maxBackoff: 2s

sandbox:
  defaults:
    namespace: sandbox
    runnerImage: sandbox-runner:1.0.0
    imagePullPolicy: IfNotPresent
    ttlSeconds: 900
    podReadyWait: 30s
    podPollInterval: 500ms
    terminationGraceSeconds: 1
    activeDeadlineSeconds: 0
    containerName: runner
    workdir: /workspace
    volumes:
      workspace:
        name: workspace
        mountPath: /workspace
        sizeLimit: "0"
      tmp:
        name: tmp
        mountPath: /tmp
        sizeLimit: 256Mi
    resources:
      requests:
        cpu: 100m
        memory: 256Mi
    limits:
      cpu: "1"
      memory: 1Gi
      ephemeralStorage: 2Gi
    labels:
      app: llm-sandbox
    annotations: {}

exec:
  defaultTimeout: 30s
  maxTimeout: 300s
  stdoutMaxBytes: 1048576
  stderrMaxBytes: 1048576
  preserveTailBytes: 4096
  exitCodeMarker:
    key: "__SBX_EXIT_CODE__"
    stream: "stderr"
  shell:
    bin: sh
    args: ["-lc"]
  env:
    allowRegex: "^[A-Z_][A-Z0-9_]*$"
  workdir:
    allowedPrefixes: ["/workspace"]

files:
  rootPrefix: /workspace
  upload:
    defaultDest: /workspace
    maxBytes: 52428800
    format: tar.gz
  download:
    defaultSrc: /workspace
    format: tar.gz
  tar:
    bin: tar
    rejectSymlinks: true
EOF
```

### Step 2: Set Environment Variables

```bash
export CONFIG_PATH=/tmp/manager-config.yaml
export SERVICE_KEYS=test-key-12345,another-key-67890
export KUBECONFIG=$HOME/.kube/config
```

### Step 3: Run Manager (Local)

```bash
cd manager-service
go run ./cmd/manager/main.go
```

### Step 4: Run Tests

Create test script `scripts/test-manager.sh`:

```bash
#!/bin/bash
set -e

BASE_URL="http://localhost:8080"
SERVICE_KEY="test-key-12345"

echo "=== Testing Manager Service ==="

# Test 1: Health check (no auth)
echo -e "\n1. Health check..."
curl -s $BASE_URL/healthz | jq .

# Test 2: Readiness check (no auth)
echo -e "\n2. Readiness check..."
curl -s $BASE_URL/readyz | jq .

# Test 3: Debug config (no auth)
echo -e "\n3. Debug config..."
curl -s $BASE_URL/debug/config | jq .meta

# Test 4: Metrics (no auth)
echo -e "\n4. Metrics..."
curl -s $BASE_URL/metrics | head -20

# Test 5: Create sandbox (with auth)
echo -e "\n5. Create sandbox..."
SESSION_ID="test-session-$(date +%s)"
curl -s -X PUT \
  -H "X-Service-Key: $SERVICE_KEY" \
  -H "Content-Type: application/json" \
  -d '{"ttlSeconds": 300}' \
  $BASE_URL/v1/sandboxes/$SESSION_ID | jq .

# Test 6: Exec command (with auth)
echo -e "\n6. Exec command..."
curl -s -X POST \
  -H "X-Service-Key: $SERVICE_KEY" \
  -H "Content-Type: application/json" \
  -d '{"cmd": ["echo", "hello world"]}' \
  $BASE_URL/v1/sandboxes/$SESSION_ID/exec | jq .

# Test 7: Upload files (with auth)
echo -e "\n7. Upload files..."
echo "test content" | tar -czf - test.txt | \
  curl -s -X POST \
    -H "X-Service-Key: $SERVICE_KEY" \
    -H "Content-Type: application/x-gzip" \
    --data-binary @- \
    "$BASE_URL/v1/sandboxes/$SESSION_ID/files/upload?dest=/workspace"

# Test 8: Download files (with auth)
echo -e "\n8. Download files..."
curl -s -X GET \
  -H "X-Service-Key: $SERVICE_KEY" \
  "$BASE_URL/v1/sandboxes/$SESSION_ID/files/download?src=/workspace" | \
  tar -tzv - | head -5

# Test 9: Touch sandbox (with auth)
echo -e "\n9. Touch sandbox..."
curl -s -X POST \
  -H "X-Service-Key: $SERVICE_KEY" \
  $BASE_URL/v1/sandboxes/$SESSION_ID/touch | jq .

# Test 10: Delete sandbox (with auth)
echo -e "\n10. Delete sandbox..."
curl -s -X DELETE \
  -H "X-Service-Key: $SERVICE_KEY" \
  $BASE_URL/v1/sandboxes/$SESSION_ID

# Test 11: Test auth failure
echo -e "\n11. Auth failure test..."
curl -s -X GET \
  -H "X-Service-Key: invalid-key" \
  $BASE_URL/v1/sandboxes/test | jq .

echo -e "\n=== All tests passed! ==="
```

Run tests:
```bash
chmod +x scripts/test-manager.sh
./scripts/test-manager.sh
```

## Cluster Deployment Testing

### Step 1: Build and Load Image

```bash
cd manager-service
./scripts/build-image.sh -r sandbox-manager -l
```

### Step 2: Deploy to Cluster

```bash
cd k8s
kubectl apply -k overlays/dev
```

### Step 3: Port Forward for Testing

```bash
kubectl -n sandbox-system port-forward svc/sandbox-manager 8080:80
```

### Step 4: Run Tests

Use the same test script as above.

## ConfigMap Hot Reload Test

```bash
# 1. Get current hash
HASH1=$(curl -s http://localhost:8080/debug/config | jq -r '.meta.currentHash')
echo "Current hash: $HASH1"

# 2. Modify ConfigMap
kubectl -n sandbox-system patch configmap sandbox-manager-config --type merge -p '
{
  "data": {
    "manager-config.yaml": "version: 1\nserver:\n  httpPort: 8080\n  requestIdHeader: X-Request-Id\n  # ... rest of config with a change ..."
  }
}'

# 3. Wait a moment and check new hash
sleep 2
HASH2=$(curl -s http://localhost:8080/debug/config | jq -r '.meta.currentHash')
echo "New hash: $HASH2"

if [ "$HASH1" != "$HASH2" ]; then
  echo "✅ ConfigMap hot reload successful!"
else
  echo "❌ ConfigMap did not reload"
fi
```

## Verification Checklist

- [ ] Dependencies resolved (go mod tidy)
- [ ] Service starts without errors
- [ ] /healthz returns 200
- [ ] /readyz returns 200 after config loaded
- [ ] /metrics returns Prometheus format
- [ ] /debug/config shows current config
- [ ] Auth rejects invalid keys
- [ ] Auth accepts valid keys
- [ ] Sandbox creation works
- [ ] Exec with exit code works
- [ ] File upload/download works
- [ ] ConfigMap hot reload works

## Known Issues

1. **Network/Proxy Issue**
   - `go mod tidy` fails with timeout when downloading fsnotify
   - Workaround: Use proxy or manually add to go.sum

2. **Handler Implementation**
   - v1 API handlers return placeholder "not yet implemented"
   - Need to implement actual business logic handlers

## Next Steps

1. Resolve Go dependencies
2. Implement v1 API handlers
3. Complete integration testing
4. Update VERSION in overlays
5. Tag and release v2.0.0
