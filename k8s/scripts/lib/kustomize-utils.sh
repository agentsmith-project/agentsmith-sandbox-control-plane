#!/bin/bash
# Kustomize utility functions

# Source common functions
# shellcheck source=lib/common.sh
SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
source "${SCRIPT_DIR}/common.sh"

# Update kustomization.yaml images using kustomize command
update_images_with_kustomize() {
    local overlay_dir="$1"
    local manager_image="$2"
    local runner_image="$3"

    if ! command_exists kustomize; then
        return 1
    fi

    (
        cd "$overlay_dir" || return 1
        kustomize edit set image \
            "$manager_image" \
            "$runner_image" 2>/dev/null
    )
}

# Update kustomization.yaml images using Python
update_images_with_python() {
    local overlay_dir="$1"
    local manager_image="$2"
    local runner_image="$3"
    local kustomization_file="${overlay_dir}/kustomization.yaml"

    if ! command_exists python3; then
        return 1
    fi

    python3 <<EOF
import yaml
import sys
from pathlib import Path

kustomization_file = Path('${kustomization_file}')
if not kustomization_file.exists():
    print(f"Error: {kustomization_file} not found", file=sys.stderr)
    sys.exit(1)

# Parse images
def parse_image(image_str):
    """Parse image string like 'registry/image:tag' into components"""
    if ':' in image_str:
        image, tag = image_str.rsplit(':', 1)
    else:
        image = image_str
        tag = None

    # Extract name from image path
    name = image.split('/')[-1]

    return {
        'name': name,
        'newName': image,
        'newTag': tag
    }

# Load kustomization
with open(kustomization_file, 'r', encoding='utf-8') as f:
    data = yaml.safe_load(f) or {}

if 'images' not in data:
    data['images'] = []

# Parse input images
images_to_update = {
    'sandbox-manager': parse_image('${manager_image}'),
    'sandbox-runner': parse_image('${runner_image}')
}

# Update or add images
images_dict = {img.get('name', ''): img for img in data.get('images', [])}

for img_name, img_config in images_to_update.items():
    images_dict[img_name] = img_config

data['images'] = list(images_dict.values())

# Write back
with open(kustomization_file, 'w', encoding='utf-8') as f:
    yaml.dump(data, f, default_flow_style=False, sort_keys=False, allow_unicode=True)
EOF
}

# Update images in kustomization.yaml (tries multiple methods)
update_kustomization_images() {
    local overlay_dir="$1"
    local manager_image="$2"
    local runner_image="$3"

    log_info "更新 overlay: $(basename "$overlay_dir")"

    # Try kustomize first
    if update_images_with_kustomize "$overlay_dir" "$manager_image" "$runner_image"; then
        log_success "使用 kustomize 更新镜像"
        return 0
    fi

    # Fallback to Python
    if update_images_with_python "$overlay_dir" "$manager_image" "$runner_image"; then
        log_success "使用 Python 更新镜像"
        return 0
    fi

    log_error "无法更新镜像：未找到 kustomize 或 python3"
    return 1
}
