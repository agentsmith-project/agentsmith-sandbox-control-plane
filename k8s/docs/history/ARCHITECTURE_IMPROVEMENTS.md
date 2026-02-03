# K8s 配置架构改进方案

## 当前问题分析

### 1. 硬编码问题
- ❌ `RUNNER_IMAGE_DEFAULT` 硬编码在 `base/manager-deployment.yaml` 中
- ❌ 包含完整的 registry 路径和版本号
- ❌ 更新版本时需要手动修改多个地方

### 2. 配置分散
- ❌ 镜像版本在 kustomization.yaml 中
- ❌ Runner 镜像在 manager-deployment.yaml 中
- ❌ 其他配置在 ConfigMap 中
- ❌ 没有统一的版本管理机制

### 3. 维护困难
- ❌ 版本更新需要修改多个文件
- ❌ 容易遗漏某些配置
- ❌ 不同环境配置不一致

## 重构方案

### 原则
1. **单一数据源**：版本信息只从 VERSION 文件读取
2. **配置集中化**：所有运行时配置放在 ConfigMap
3. **Base 保持通用**：Base 中不包含环境特定配置
4. **Overlay 覆盖**：环境特定配置通过 overlay 覆盖

### 改进方案

#### 1. 将 Runner 镜像移到 ConfigMap

**Base ConfigMap** (`k8s/base/configmap.yaml`):
```yaml
data:
  runner-image-default: "sandbox-runner:1.0.0"  # 只包含镜像名称和默认版本
```

**Overlay Patch** (`k8s/overlays/dev/patches/configmap-runner-image.yaml`):
```yaml
data:
  runner-image-default: "harbor.pullot.com:28443/agentsmith/sandbox-runner:1.0.0"
```

#### 2. Manager Deployment 从 ConfigMap 读取

**Base Manager Deployment**:
```yaml
env:
- name: RUNNER_IMAGE_DEFAULT
  valueFrom:
    configMapKeyRef:
      name: sandbox-config
      key: runner-image-default
```

#### 3. 统一版本更新脚本

`update-images.sh` 应该：
1. 读取 VERSION 文件
2. 更新 kustomization.yaml 中的镜像
3. 更新所有 overlay 的 ConfigMap patch 中的 runner-image-default

## 实施步骤

1. 修改 base/configmap.yaml - 添加 runner-image-default
2. 修改 base/manager-deployment.yaml - 从 ConfigMap 读取
3. 为每个 overlay 创建 configmap-runner-image.yaml patch
4. 更新 update-images.sh - 同时更新 ConfigMap patches
5. 验证所有环境
