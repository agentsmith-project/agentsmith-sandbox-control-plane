# K8s 配置架构重构总结

## 重构目标

从结构上解决版本管理问题，避免"头疼医头，脚疼医脚"的临时修复。

## 核心问题

### 问题 1: 硬编码配置
- ❌ `RUNNER_IMAGE_DEFAULT` 硬编码在 `base/manager-deployment.yaml`
- ❌ 包含完整的 registry 路径和版本号
- ❌ Base 中不应该包含环境特定配置

### 问题 2: 配置分散
- ❌ 镜像版本在 kustomization.yaml
- ❌ Runner 镜像在 manager-deployment.yaml
- ❌ 其他配置在 ConfigMap
- ❌ 更新版本需要修改多个地方

### 问题 3: 版本不一致
- ❌ 容易遗漏某些配置
- ❌ 不同环境配置可能不一致
- ❌ 没有统一的版本管理机制

## 重构方案

### 设计原则

1. **单一数据源 (Single Source of Truth)**
   - 版本信息只从 VERSION 文件读取
   - 所有配置通过脚本自动更新

2. **配置集中化 (Centralized Configuration)**
   - 所有运行时配置放在 ConfigMap
   - Base 中只包含通用配置
   - 环境特定配置通过 overlay 覆盖

3. **Base 保持通用 (Base Stays Generic)**
   - Base 中不包含 registry 路径
   - Base 中不包含环境特定值
   - Base 中只使用占位符或默认值

4. **Overlay 覆盖 (Overlay Overrides)**
   - 环境特定配置通过 overlay patch 覆盖
   - 版本更新通过脚本自动同步

## 实施内容

### 1. ConfigMap 重构

**Base ConfigMap** (`k8s/base/configmap.yaml`):
```yaml
data:
  runner-image-default: "sandbox-runner:1.0.0"  # 只包含镜像名称和默认版本
```

**Overlay Patch** (`k8s/overlays/*/patches/configmap-runner-image.yaml`):
```yaml
data:
  runner-image-default: "harbor.pullot.com:28443/agentsmith/sandbox-runner:1.0.0"
```

### 2. Manager Deployment 重构

**修改前**:
```yaml
env:
- name: RUNNER_IMAGE_DEFAULT
  value: "harbor.pullot.com:28443/agentsmith/sandbox-runner:1.0.0"  # ❌ 硬编码
```

**修改后**:
```yaml
env:
- name: RUNNER_IMAGE_DEFAULT
  valueFrom:
    configMapKeyRef:
      name: sandbox-config
      key: runner-image-default  # ✅ 从 ConfigMap 读取
```

### 3. 统一版本更新脚本

`update-images.sh` 现在会：
1. 读取 VERSION 文件
2. 更新 kustomization.yaml 中的镜像
3. 更新所有 overlay 的 ConfigMap patch 中的 runner-image-default
4. 确保 patch 包含在 kustomization.yaml 中

### 4. 文件结构

```
k8s/
├── base/
│   ├── configmap.yaml                    # ✅ 添加 runner-image-default (通用值)
│   └── manager-deployment.yaml           # ✅ 从 ConfigMap 读取
├── overlays/
│   ├── dev/
│   │   ├── kustomization.yaml           # ✅ 包含 configmap-runner-image.yaml
│   │   └── patches/
│   │       └── configmap-runner-image.yaml  # ✅ 环境特定配置
│   ├── staging/
│   │   └── patches/
│   │       └── configmap-runner-image.yaml
│   └── production/
│       └── patches/
│           └── configmap-runner-image.yaml
└── scripts/
    ├── update-images.sh                  # ✅ 同时更新镜像和 ConfigMap
    └── test-version-update.sh            # ✅ 新增：验证版本一致性
```

## 优势

### 1. 单一数据源
- ✅ 版本信息只从 VERSION 文件读取
- ✅ 更新版本只需修改 VERSION 文件
- ✅ 脚本自动同步到所有配置

### 2. 配置集中化
- ✅ 所有运行时配置在 ConfigMap
- ✅ 环境特定配置通过 overlay 覆盖
- ✅ 易于管理和审计

### 3. 自动化
- ✅ `update-images.sh` 自动更新所有配置
- ✅ 版本一致性自动保证
- ✅ 减少人为错误

### 4. 可维护性
- ✅ 清晰的配置层次结构
- ✅ 易于理解和修改
- ✅ 符合 K8s 最佳实践

## 验证

使用 `test-version-update.sh` 脚本可以验证：
1. VERSION 文件完整性
2. kustomization.yaml 版本一致性
3. ConfigMap patch 版本正确性
4. 部署的 ConfigMap 值正确性
5. Manager Deployment 配置正确性

## 后续改进建议

1. **使用 Kustomize replacements** (Kustomize 4.5+)
   - 更强大的配置替换能力
   - 减少 patch 文件数量

2. **版本管理脚本**
   - 创建统一的版本管理脚本
   - 支持版本升级、回滚等操作

3. **CI/CD 集成**
   - 在 CI/CD 中自动验证版本一致性
   - 自动更新配置并部署

4. **文档完善**
   - 添加配置管理文档
   - 添加版本升级指南
