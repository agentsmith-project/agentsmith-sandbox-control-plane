#!/bin/bash
# Common utility functions for K8s deployment scripts

# Color output
RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
BLUE='\033[0;34m'
NC='\033[0m' # No Color

# Logging functions
log_info() {
    echo -e "${BLUE}[INFO]${NC} $*"
}

log_success() {
    echo -e "${GREEN}[✓]${NC} $*"
}

log_warning() {
    echo -e "${YELLOW}[⚠]${NC} $*"
}

log_error() {
    echo -e "${RED}[✗]${NC} $*" >&2
}

# Check if command exists
command_exists() {
    command -v "$1" >/dev/null 2>&1
}

# Check kustomize availability
check_kustomize() {
    if command_exists kustomize; then
        echo "kustomize"
    elif kubectl version --client &>/dev/null && kubectl kustomize --help &>/dev/null 2>&1; then
        echo "kubectl kustomize"
    else
        return 1
    fi
}

# Load configuration file
load_config() {
    local config_file="${1:-}"
    if [ -f "$config_file" ]; then
        # shellcheck source=/dev/null
        source "$config_file"
        return 0
    fi
    return 1
}

# Get project root directory
get_project_root() {
    local script_dir="${1:-$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)}"
    echo "$script_dir"
}

# Get K8s directory
get_k8s_dir() {
    local script_dir="${1:-$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)}"
    echo "$script_dir"
}

# Read version from VERSION file
read_version() {
    local version_file="$1"
    if [ -f "$version_file" ]; then
        # Read and trim whitespace
        local version
        version=$(cat "$version_file" | tr -d '[:space:]')
        if [ -n "$version" ]; then
            echo "$version"
        else
            echo ""
        fi
    else
        echo ""
    fi
}

# Build full registry path
build_registry_path() {
    local registry="${1:-localhost:5001}"
    local harbor_project="${2:-}"
    
    if [[ "$registry" == *"harbor"* ]] && [ -n "$harbor_project" ]; then
        echo "${registry}/${harbor_project}"
    else
        echo "$registry"
    fi
}

# Check Kubernetes cluster connection
check_k8s_connection() {
    if ! kubectl cluster-info &>/dev/null; then
        log_error "无法连接到 Kubernetes 集群"
        return 1
    fi
    return 0
}

# Validate overlay directory
validate_overlay() {
    local overlay_dir="$1"
    if [ ! -d "$overlay_dir" ]; then
        log_error "Overlay 目录不存在: $overlay_dir"
        return 1
    fi
    if [ ! -f "${overlay_dir}/kustomization.yaml" ]; then
        log_error "kustomization.yaml 不存在: ${overlay_dir}/kustomization.yaml"
        return 1
    fi
    return 0
}

# Handle errors with cleanup
handle_error() {
    local exit_code="${1:-1}"
    local message="${2:-发生错误}"
    log_error "$message"
    exit "$exit_code"
}

# Validate required parameters
validate_required() {
    local param_name="$1"
    local param_value="$2"
    
    if [ -z "$param_value" ]; then
        log_error "必需参数缺失: $param_name"
        return 1
    fi
    return 0
}

# Check prerequisites
check_prerequisites() {
    local missing_tools=()
    
    for tool in "$@"; do
        if ! command_exists "$tool"; then
            missing_tools+=("$tool")
        fi
    done
    
    if [ ${#missing_tools[@]} -gt 0 ]; then
        log_error "缺少必需工具: ${missing_tools[*]}"
        return 1
    fi
    return 0
}

# Validate file exists
validate_file() {
    local file_path="$1"
    local description="${2:-文件}"
    
    if [ ! -f "$file_path" ]; then
        log_error "${description}不存在: $file_path"
        return 1
    fi
    return 0
}

# Validate directory exists
validate_directory() {
    local dir_path="$1"
    local description="${2:-目录}"
    
    if [ ! -d "$dir_path" ]; then
        log_error "${description}不存在: $dir_path"
        return 1
    fi
    return 0
}
