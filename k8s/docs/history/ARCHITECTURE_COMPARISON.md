# 架构改进前后对比

## 改进前的问题

### 1. 硬编码配置
```yaml
# base/manager-deployment.yaml
env:
- name: RUNNER_IMAGE_DEFAULT
  value: "harbor.pullot.com:28443/agentsmith/sandbox-runner:1.2.0"  # ❌ 硬编码
```

**问题**:
- Base 中包含环境特定配置（registry 路径）
- 版本更新需要手动修改
- 容易遗漏或出错

### 2. 配置分散
- 镜像版本在 `kustomization.yaml`
- Runner 镜像在 `manager-deployment.yaml`
- 其他配置在 `ConfigMap`
- 更新需要修改多个地方

### 3. 版本不一致风险
- 手动更新容易遗漏
- 不同环境可能不一致
- 没有自动化验证

## 改进后的架构

### 1. 配置集中化
```yaml
# base/configmap.yaml
data:
  runner-image-default: "sandbox-runner:1.0.0"  # ✅ 通用值

# base/manager-deployment.yaml
env:
- name: RUNNER_IMAGE_DEFAULT
  valueFrom:
    configMapKeyRef:
      name: sandbox-config
      key: runner-image-default  # ✅ 从 ConfigMap 读取

# overlays/*/patches/configmap-runner-image.yaml
data:
  runner-image-default: "harbor.pullot.com:28443/agentsmith/sandbox-runner:1.0.0"  # ✅ 环境特定值
```

**优势**:
- Base 保持通用，不包含环境特定配置
- 配置集中管理
- 易于维护和审计

### 2. 自动化版本更新
```bash
# update-images.sh 现在会：
1. 读取 VERSION 文件
2. 更新 kustomization.yaml 中的镜像
3. 更新所有 overlay 的 ConfigMap patch
4. 确保配置一致性
```

**优势**:
- 单一数据源（VERSION 文件）
- 自动同步所有配置
- 减少人为错误

### 3. 验证机制
```bash
# test-version-update.sh
- 检查 VERSION 文件
- 验证 kustomization.yaml 版本
- 验证 ConfigMap patch 版本
- 验证部署的配置
```

**优势**:
- 自动化验证
- 早期发现问题
- 确保配置一致性

## 架构对比

| 方面 | 改进前 | 改进后 |
|------|--------|--------|
| **配置位置** | 分散在多个文件 | 集中在 ConfigMap |
| **Base 配置** | 包含环境特定值 | 只包含通用值 |
| **版本更新** | 手动修改多个地方 | 自动同步 |
| **一致性** | 容易不一致 | 自动保证一致 |
| **可维护性** | 低 | 高 |
| **可扩展性** | 困难 | 容易 |

## 最佳实践遵循

### ✅ 单一职责原则
- Base: 通用配置
- Overlay: 环境特定配置
- Script: 自动化更新

### ✅ DRY 原则
- 版本信息只从 VERSION 文件读取
- 配置通过脚本自动同步

### ✅ 配置与代码分离
- 运行时配置在 ConfigMap
- 构建时配置在 Dockerfile ARG

### ✅ 环境隔离
- 不同环境使用不同的 overlay
- 环境特定配置通过 patch 覆盖

### ✅ 可观测性
- 验证脚本检查配置一致性
- 清晰的日志输出

## 总结

这次重构从**架构层面**解决了问题，而不是临时修复：

1. **配置管理**: 从硬编码改为 ConfigMap + overlay patch
2. **版本管理**: 从手动更新改为自动化脚本
3. **一致性**: 从容易出错改为自动保证
4. **可维护性**: 从低到高

符合 Kubernetes 和 DevOps 最佳实践。
