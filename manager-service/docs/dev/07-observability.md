# Task G: /readyz + /metrics Endpoints

**Status:** ✅ Completed

## Summary

Implemented `/readyz` health check endpoint with K8s client and config validation, plus Prometheus-compatible `/metrics` endpoint.

## Implementation

### Files Created

- `internal/observability/metrics.go` - Metrics collection and Prometheus formatter
- `internal/observability/health.go` - Health check handlers
- `internal/observability/logging.go` - Structured logging

### /readyz Endpoint

**Path:** `GET /readyz`

**Purpose:** Kubernetes readiness probe - returns 200 only when service is ready to handle traffic.

**Readiness Conditions:**
1. K8s client initialized successfully
2. At least one successful configuration load

**Response (Ready - 200):**
```json
{
  "ready": true,
  "configLoaded": true,
  "k8sConnected": true,
  "message": "Service is ready"
}
```

**Response (Not Ready - 503):**
```json
{
  "ready": false,
  "configLoaded": false,
  "k8sConnected": false,
  "message": "Service is not ready: check_0, check_1"
}
```

### /healthz Endpoint

**Path:** `GET /healthz`

**Purpose:** Kubernetes liveness probe - returns 200 if process is alive.

**Response (200):**
```json
{
  "status": "ok",
  "time": "2026-01-21T10:00:00Z"
}
```

### /metrics Endpoint

**Path:** `GET /metrics`

**Purpose:** Prometheus metrics scraping.

**Format:** Prometheus text-based exposition format.

**Metrics:**

#### HTTP Metrics
```
# HTTP request count by method, path, status
http_request_total{method="POST",path="/v1/sandboxes",status="200"} 1234

# HTTP request duration histogram
http_request_duration_seconds_bucket{method="POST",path="/v1/sandboxes",le="0.005"} 10
http_request_duration_seconds_bucket{method="POST",path="/v1/sandboxes",le="0.01"} 25
http_request_duration_seconds_bucket{method="POST",path="/v1/sandboxes",le="+Inf"} 1000
http_request_duration_seconds_sum{method="POST",path="/v1/sandboxes"} 45.3
http_request_duration_seconds_count{method="POST",path="/v1/sandboxes"} 1000
```

#### Business Metrics
```
sandbox_create_total 543
sandbox_touch_total 1234
sandbox_exec_total 5678
sandbox_upload_total 234
sandbox_download_total 456
sandbox_delete_total 123
```

#### Configuration Metrics
```
config_reload_success_total 5
config_reload_failure_total 1
config_hash_info{hash="abc123..."} 1
config_loaded_at_timestamp 1705833600
```

#### K8s Metrics
```
k8s_api_fail_total{operation="Create"} 2
k8s_api_fail_total{operation="Get"} 1
```

### Configuration

```yaml
server:
  metrics:
    enabled: true
    path: /metrics
    requireServiceKey: false
```

**Note:** Setting `requireServiceKey: true` adds authentication to the metrics endpoint.

### Kubernetes Probes

```yaml
readinessProbe:
  httpGet:
    path: /readyz
    port: 8080
  initialDelaySeconds: 5
  periodSeconds: 5
  timeoutSeconds: 3
  failureThreshold: 3

livenessProbe:
  httpGet:
    path: /healthz
    port: 8080
  initialDelaySeconds: 15
  periodSeconds: 15
  timeoutSeconds: 3
  failureThreshold: 3
```

### Prometheus Scrape Config

```yaml
scrape_configs:
  - job_name: 'sandbox-manager'
    kubernetes_sd_configs:
      - role: pod
        namespaces:
          names:
            - sandbox-system
    relabel_configs:
      - source_labels: [__meta_kubernetes_pod_annotation_prometheus_io_scrape]
        action: keep
        regex: true
      - source_labels: [__meta_kubernetes_pod_annotation_prometheus_io_path]
        action: replace
        target_label: __metrics_path__
        regex: (.+)
      - source_labels: [__address__, __meta_kubernetes_pod_annotation_prometheus_io_port]
        action: replace
        regex: ([^:]+)(?::\d+)?;(\d+)
        replacement: $1:$2
        target_label: __address__
```

### Metrics Registry

The `MetricsRegistry` tracks:

- **HTTP Metrics:** Request count, duration histogram by route
- **Business Metrics:** Create/touch/exec/upload/download/delete counts
- **Config Metrics:** Reload success/failure, current hash
- **K8s Metrics:** API call failures by operation

Thread-safe with mutex protection for concurrent access.

## Validation

### Acceptance Criteria

- [x] `/readyz` returns 503 when config not loaded
- [x] `/readyz` returns 200 when service ready
- [x] `/metrics` returns Prometheus format
- [x] Metrics include HTTP, business, config, K8s data

### Example Queries

**Check readiness:**
```bash
kubectl exec -n sandbox-system deployment/sandbox-manager -- curl -s http://localhost:8080/readyz
```

**Scrape metrics:**
```bash
kubectl exec -n sandbox-system deployment/sandbox-manager -- curl -s http://localhost:8080/metrics
```

**Prometheus query for error rate:**
```promql
rate(http_request_total{status=~"5.."}[5m])
```

**Prometheus query for request duration:**
```promql
histogram_quantile(0.95, rate(http_request_duration_seconds_bucket[5m]))
```

## Next Steps

See [08-debug-config.md](./08-debug-config.md) for the debug config endpoint.
