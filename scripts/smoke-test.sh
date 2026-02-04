#!/bin/bash
set -euo pipefail

#=============================================================================
# mbos-sandbox-v1 Smoke Test Script
#
# 用途: 验证沙盒系统的核心功能可正常运行
# 使用: ./scripts/smoke-test.sh [--help]
#
# 测试覆盖:
#   1. 环境检查 (Docker, kind, 磁盘空间)
#   2. 镜像构建 (manager, runner)
#   3. Kind 集群创建
#   4. 镜像加载到集群
#   5. 应用部署
#   6. Manager 服务健康检查
#   7. 沙盒创建和命令执行
#   8. 资源清理验证
#=============================================================================

# 配置
SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"
PROJECT_DIR="$(cd "${SCRIPT_DIR}/.." && pwd)"
VERSION="${VERSION:-$(cat "$PROJECT_DIR/VERSION" 2>/dev/null || echo dev)}"
CLUSTER_NAME="${CLUSTER_NAME:-sandbox-cluster}"
MANAGER_URL="${MANAGER_URL:-http://localhost:8080}"
SERVICE_KEY="${SERVICE_KEY:-dev-key-12345}"

# 代理配置 (可从环境变量读取)
HTTP_PROXY="${HTTP_PROXY:-}"
HTTPS_PROXY="${HTTPS_PROXY:-}"
NO_PROXY="${NO_PROXY:-localhost,127.0.0.1}"

# 颜色输出
RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
BLUE='\033[0;34m'
NC='\033[0m'

# 统计
TOTAL_TESTS=0
PASSED_TESTS=0
FAILED_TESTS=0

# 辅助函数
log_info()    { echo -e "${BLUE}[INFO]${NC} $1"; }
log_success() { echo -e "${GREEN}[✓]${NC} $1"; }
log_error()   { echo -e "${RED}[✗]${NC} $1"; }
log_warn()    { echo -e "${YELLOW}[!]${NC} $1"; }
log_step()    { echo -e "\n${BLUE}=== $1 ===${NC}"; }

pass_test() {
    ((PASSED_TESTS++))
    ((TOTAL_TESTS++))
    log_success "$1"
}

fail_test() {
    ((FAILED_TESTS++))
    ((TOTAL_TESTS++))
    log_error "$1"
}

# 清理函数
cleanup_on_error() {
    log_warn "Interrupted, cleaning up..."
    cleanup
    exit 1
}

cleanup() {
    log_step "Cleaning up resources"

    # 停止端口转发
    if [ -f /tmp/sandbox-pf.pid ]; then
        kill $(cat /tmp/sandbox-pf.pid) 2>/dev/null || true
        rm -f /tmp/sandbox-pf.pid
    fi

    # 删除 Kind 集群
    if kind get clusters | grep -q "$CLUSTER_NAME"; then
        log_info "Deleting kind cluster..."
        kind delete cluster --name "$CLUSTER_NAME" 2>/dev/null || true
    fi

    # 删除 MinIO
    if docker ps -q -f name=minio-sandbox-test | grep -q .; then
        log_info "Removing MinIO container..."
        docker rm -f minio-sandbox-test 2>/dev/null || true
    fi

    # 清理临时文件
    rm -f /tmp/sandbox-pf.log

    log_info "Cleanup complete"
}

# 显示帮助
show_help() {
    cat << EOF
Usage: $0 [OPTIONS]

mbos-sandbox-v1 Smoke Test Script

OPTIONS:
    -h, --help              显示此帮助信息
    -k, --keep-cluster      测试后保留集群 (用于调试)
    -v, --verbose           详细输出
    --no-build              跳过镜像构建 (使用已有镜像)
    --no-cluster            跳过集群创建 (使用已有集群)
    --skip-cleanup          测试后不清理资源
    --manager-url URL       Manager 服务 URL (默认: http://localhost:8080)
    --service-key KEY       Service Key (默认: dev-key-12345)

ENVIRONMENT VARIABLES:
    HTTP_PROXY              HTTP 代理地址
    HTTPS_PROXY             HTTPS 代理地址
    NO_PROXY                不代理的地址列表
    VERSION                 镜像版本 (默认: dev 或从 VERSION 文件读取)

EXAMPLES:
    # 完整冒烟测试
    $0

    # 使用代理
    HTTP_PROXY=http://proxy.example.com:8080 $0

    # 跳过镜像构建和集群创建 (快速测试)
    $0 --no-build --no-cluster

    # 测试后保留集群
    $0 --keep-cluster

EOF
    exit 0
}

# 参数解析
KEEP_CLUSTER=false
VERBOSE=false
NO_BUILD=false
NO_CLUSTER=false
SKIP_CLEANUP=false

while [[ $# -gt 0 ]]; do
    case $1 in
        -h|--help)    show_help ;;
        -k|--keep-cluster) KEEP_CLUSTER=true ;;
        -v|--verbose) VERBOSE=true ;;
        --no-build)   NO_BUILD=true ;;
        --no-cluster) NO_CLUSTER=true ;;
        --skip-cleanup) SKIP_CLEANUP=true ;;
        --manager-url) MANAGER_URL="$2"; shift ;;
        --service-key) SERVICE_KEY="$2"; shift ;;
        *) log_error "Unknown option: $1"; show_help ;;
    esac
    shift
done

# 设置清理陷阱
trap cleanup_on_error INT TERM

#=============================================================================
# 测试函数
#=============================================================================

# 1. 环境检查
test_environment() {
    log_step "Step 1: Environment Check"

    # Docker 检查
    if docker info >/dev/null 2>&1; then
        pass_test "Docker is running"
    else
        fail_test "Docker is not running"
        return 1
    fi

    # kind 检查
    if kind version >/dev/null 2>&1; then
        KIND_VERSION=$(kind version | awk '{print $2}')
        pass_test "kind is installed (version: $KIND_VERSION)"
    else
        fail_test "kind is not installed"
        return 1
    fi

    # 磁盘空间检查
    DISK_AVAILABLE=$(df -BG "$PROJECT_DIR" | tail -1 | awk '{print $4}' | sed 's/G//')
    if [ "$DISK_AVAILABLE" -gt 20 ]; then
        pass_test "Disk space: ${DISK_AVAILABLE}GB available"
    else
        fail_test "Insufficient disk space: ${DISK_AVAILABLE}GB (need >20GB)"
        return 1
    fi

    # Go 版本检查 (仅构建时需要)
    if [ "$NO_BUILD" = false ]; then
        if command -v go >/dev/null 2>&1; then
            GO_VERSION=$(go version | awk '{print $3}')
            pass_test "Go version: $GO_VERSION"
        else
            fail_test "Go is not installed (required for building)"
            return 1
        fi
    fi
}

# 2. 镜像构建
test_build_images() {
    if [ "$NO_BUILD" = true ]; then
        log_step "Step 2: Build Images (skipped)"
        return 0
    fi

    log_step "Step 2: Build Images"

    local build_args=()
    if [ -n "$HTTP_PROXY" ]; then
        build_args+=(--pull-proxy "$HTTP_PROXY" --build-proxy off)
        export HTTP_PROXY HTTPS_PROXY NO_PROXY
    fi

    # 构建 manager
    log_info "Building manager image..."
    cd "$PROJECT_DIR/manager-service"
    if docker buildx build --load -t "sandbox-manager:$VERSION" -f Dockerfile . "${build_args[@]}" >/dev/null 2>&1; then
        pass_test "Manager image built successfully"
    else
        fail_test "Failed to build manager image"
        return 1
    fi

    # 构建 runner
    log_info "Building runner image..."
    cd "$PROJECT_DIR/runner-service"
    if docker buildx build --load -t "sandbox-runner:$VERSION" -f Dockerfile . "${build_args[@]}" >/dev/null 2>&1; then
        pass_test "Runner image built successfully"
    else
        fail_test "Failed to build runner image"
        return 1
    fi

    cd "$PROJECT_DIR"
}

# 3. 创建 Kind 集群
test_create_cluster() {
    if [ "$NO_CLUSTER" = true ]; then
        log_step "Step 3: Create Kind Cluster (skipped)"
        return 0
    fi

    log_step "Step 3: Create Kind Cluster"

    # 删除旧集群
    kind delete cluster --name "$CLUSTER_NAME" 2>/dev/null || true

    # 创建新集群
    log_info "Creating kind cluster: $CLUSTER_NAME"
    if kind create cluster --name "$CLUSTER_NAME" --image kindest/node:v1.31.0 >/dev/null 2>&1; then
        pass_test "Kind cluster created"
    else
        fail_test "Failed to create kind cluster"
        return 1
    fi

    # 验证集群
    if kubectl cluster-info >/dev/null 2>&1; then
        pass_test "Cluster is accessible"
    else
        fail_test "Cluster is not accessible"
        return 1
    fi
}

# 4. 加载镜像
test_load_images() {
    log_step "Step 4: Load Images to Cluster"

    # 加载 manager
    if kind load docker-image "sandbox-manager:$VERSION" --name "$CLUSTER_NAME" >/dev/null 2>&1; then
        pass_test "Manager image loaded to cluster"
    else
        fail_test "Failed to load manager image"
        return 1
    fi

    # 加载 runner
    if kind load docker-image "sandbox-runner:$VERSION" --name "$CLUSTER_NAME" >/dev/null 2>&1; then
        pass_test "Runner image loaded to cluster"
    else
        fail_test "Failed to load runner image"
        return 1
    fi
}

# 5. 部署应用
test_deploy() {
    log_step "Step 5: Deploy Application"

    cd "$PROJECT_DIR"

    # 创建命名空间
    kubectl create namespace sandbox-system --dry-run=client -o yaml | kubectl apply -f - >/dev/null 2>&1
    kubectl create namespace sandbox --dry-run=client -o yaml | kubectl apply -f - >/dev/null 2>&1

    # 部署应用
    if kubectl apply -k k8s/base >/dev/null 2>&1; then
        pass_test "Application deployed"
    else
        fail_test "Failed to deploy application"
        return 1
    fi

    # 等待 Pod 就绪
    log_info "Waiting for manager pod to be ready..."
    if kubectl wait --for=condition=ready pod -l app=sandbox-manager -n sandbox-system --timeout=120s >/dev/null 2>&1; then
        pass_test "Manager pod is ready"
    else
        fail_test "Manager pod failed to become ready"
        return 1
    fi
}

# 6. 启动 MinIO
test_start_minio() {
    log_step "Step 6: Start MinIO (for storage)"

    # 删除旧容器
    docker rm -f minio-sandbox-test 2>/dev/null || true

    # 启动 MinIO
    if docker run -d --name minio-sandbox-test \
        -p 127.0.0.1:9000:9000 -p 127.0.0.1:9001:9001 \
        -e MINIO_ROOT_USER=minioadmin \
        -e MINIO_ROOT_PASSWORD=minioadmin \
        quay.io/minio/minio server /data --console-address ":9001" >/dev/null 2>&1; then
        pass_test "MinIO container started"
    else
        fail_test "Failed to start MinIO"
        return 1
    fi

    # 等待 MinIO 就绪
    sleep 3
    if curl -s http://localhost:9000/minio/health/live | grep -q OK; then
        pass_test "MinIO is ready"
    else
        fail_test "MinIO health check failed"
        return 1
    fi
}

# 7. 端口转发
test_port_forward() {
    log_step "Step 7: Setup Port Forward"

    # 停止旧的端口转发
    pkill -f "port-forward.*sandbox-manager" 2>/dev/null || true

    # 启动端口转发
    kubectl port-forward -n sandbox-system svc/sandbox-manager 8080:80 \
        --address 127.0.0.1 > /tmp/sandbox-pf.log 2>&1 &
    local pf_pid=$!
    echo $pf_pid > /tmp/sandbox-pf.pid

    # 等待端口转发就绪
    sleep 3

    # 验证连接
    if curl -s "$MANAGER_URL/healthz" | grep -q '"status":"ok"'; then
        pass_test "Port forward established"
    else
        fail_test "Port forward failed"
        return 1
    fi
}

# 8. API 健康检查
test_health_checks() {
    log_step "Step 8: Health Checks"

    local endpoints=(
        "$MANAGER_URL/healthz"
        "$MANAGER_URL/readyz"
        "$MANAGER_URL/metrics"
        "$MANAGER_URL/debug/config"
    )

    for endpoint in "${endpoints[@]}"; do
        if curl -s "$endpoint" >/dev/null 2>&1; then
            pass_test "Health check passed: $endpoint"
        else
            fail_test "Health check failed: $endpoint"
        fi
    done
}

# 9. 沙盒功能测试
test_sandbox() {
    log_step "Step 9: Sandbox Functionality Tests"

    local session_id="smoke-test-$(date +%s)"
    local pod_name=""

    # 测试 1: 创建沙盒
    log_info "Creating sandbox: $session_id"
    local response=$(curl -s -X PUT "${MANAGER_URL}/v1/sandboxes/${session_id}" \
        -H "X-Service-Key: $SERVICE_KEY" \
        -H "Content-Type: application/json" \
        -d '{"ttlSeconds": 900, "containerName": "runner", "workdir": "/workspace"}')

    if echo "$response" | grep -q '"podName"'; then
        pod_name=$(echo "$response" | grep -o '"podName":"[^"]*"' | cut -d'"' -f4)
        pass_test "Sandbox created: $pod_name"
    else
        fail_test "Failed to create sandbox"
        return 1
    fi

    # 等待 Pod 就绪
    sleep 8

    # 测试 2: Echo 命令
    local result=$(curl -s -X POST "${MANAGER_URL}/v1/sandboxes/${session_id}/exec" \
        -H "X-Service-Key: $SERVICE_KEY" \
        -H "Content-Type: application/json" \
        -d '{"cmd": ["echo", "Smoke test"], "timeoutSeconds": 10}')

    if echo "$result" | grep -q '"exitCode":0'; then
        pass_test "Echo command successful"
    else
        fail_test "Echo command failed"
    fi

    # 测试 3: PWD 命令
    result=$(curl -s -X POST "${MANAGER_URL}/v1/sandboxes/${session_id}/exec" \
        -H "X-Service-Key: $SERVICE_KEY" \
        -H "Content-Type: application/json" \
        -d '{"cmd": ["pwd"], "timeoutSeconds": 10}')

    if echo "$result" | grep -q '"exitCode":0' && echo "$result" | grep -q '/workspace'; then
        pass_test "PWD command successful"
    else
        fail_test "PWD command failed"
    fi

    # 测试 4: LS 命令
    result=$(curl -s -X POST "${MANAGER_URL}/v1/sandboxes/${session_id}/exec" \
        -H "X-Service-Key: $SERVICE_KEY" \
        -H "Content-Type: application/json" \
        -d '{"cmd": ["ls", "-la"], "timeoutSeconds": 10}')

    if echo "$result" | grep -q '"exitCode":0'; then
        pass_test "LS command successful"
    else
        fail_test "LS command failed"
    fi

    # 测试 5: Touch (TTL 续期)
    if curl -s -X POST "${MANAGER_URL}/v1/sandboxes/${session_id}/touch" \
        -H "X-Service-Key: $SERVICE_KEY" >/dev/null 2>&1; then
        pass_test "Touch (TTL extension) successful"
    else
        fail_test "Touch failed"
    fi

    # 测试 6: 删除沙盒
    if curl -s -X DELETE "${MANAGER_URL}/v1/sandboxes/${session_id}" \
        -H "X-Service-Key: $SERVICE_KEY" >/dev/null 2>&1; then
        pass_test "Sandbox deleted"
    else
        fail_test "Failed to delete sandbox"
    fi
}

# 10. 验证清理
test_cleanup_verification() {
    log_step "Step 10: Cleanup Verification"

    sleep 5

    # 检查 Pod 被删除
    local pods=$(kubectl get pods -n sandbox --no-headers 2>/dev/null | wc -l)
    if [ "$pods" -eq 0 ]; then
        pass_test "All sandbox pods cleaned up"
    else
        log_warn "Some pods still exist (may be terminating)"
    fi
}

#=============================================================================
# 主流程
#=============================================================================

main() {
    echo -e "${BLUE}╔════════════════════════════════════════════════════════════╗${NC}"
    echo -e "${BLUE}║          mbos-sandbox-v1 Smoke Test                        ║${NC}"
    echo -e "${BLUE}╚════════════════════════════════════════════════════════════╝${NC}"
    echo ""
    log_info "Version: $VERSION"
    log_info "Cluster: $CLUSTER_NAME"
    log_info "Manager URL: $MANAGER_URL"

    # 运行测试
    test_environment || { log_error "Environment check failed"; cleanup; exit 1; }
    test_build_images || { log_error "Build failed"; cleanup; exit 1; }
    test_create_cluster || { log_error "Cluster creation failed"; cleanup; exit 1; }
    test_load_images || { log_error "Image load failed"; cleanup; exit 1; }
    test_deploy || { log_error "Deployment failed"; cleanup; exit 1; }
    test_start_minio || { log_error "MinIO start failed"; cleanup; exit 1; }
    test_port_forward || { log_error "Port forward failed"; cleanup; exit 1; }
    test_health_checks || { log_error "Health checks failed"; cleanup; exit 1; }
    test_sandbox || { log_error "Sandbox tests failed"; cleanup; exit 1; }
    test_cleanup_verification

    # 显示结果
    echo ""
    echo -e "${BLUE}╔════════════════════════════════════════════════════════════╗${NC}"
    echo -e "${BLUE}║                      Test Results                           ║${NC}"
    echo -e "${BLUE}╠════════════════════════════════════════════════════════════╣${NC}"
    echo -e "${BLUE}║  Total Tests: ${TOTAL_TESTS}                                              ║${NC}"
    echo -e "${GREEN}║  Passed:     ${PASSED_TESTS}                                              ║${NC}"
    if [ $FAILED_TESTS -gt 0 ]; then
        echo -e "${RED}║  Failed:     ${FAILED_TESTS}                                              ║${NC}"
    else
        echo -e "${BLUE}║  Failed:     ${FAILED_TESTS}                                              ║${NC}"
    fi
    echo -e "${BLUE}╚════════════════════════════════════════════════════════════╝${NC}"
    echo ""

    if [ $FAILED_TESTS -eq 0 ]; then
        log_success "All smoke tests passed!"
        if [ "$KEEP_CLUSTER" = false ] && [ "$SKIP_CLEANUP" = false ]; then
            cleanup
        elif [ "$KEEP_CLUSTER" = true ]; then
            log_warn "Cluster kept for debugging (use: kind delete cluster --name $CLUSTER_NAME to clean up)"
        elif [ "$SKIP_CLEANUP" = true ]; then
            log_warn "Cleanup skipped (remember to clean up manually)"
        fi
        exit 0
    else
        log_error "Some tests failed"
        cleanup
        exit 1
    fi
}

# 运行主流程
main
