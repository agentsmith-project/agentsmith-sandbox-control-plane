# 代码库清理计划

## 需要删除的文件

### 1. 临时修复脚本（已被重构方案替代）

这些脚本是临时解决方案，现在架构已经重构，不再需要：

- ❌ `scripts/fix-runner-image.sh` - 临时修复脚本
- ❌ `scripts/force-update-runner-image.sh` - 强制更新脚本（功能已被 `update-images.sh` 替代）
- ❌ `scripts/force-update-runner-with-new-tag.sh` - 强制更新脚本（功能已被 `update-images.sh` 替代）

**原因**：
- 这些脚本使用 `kubectl set env` 直接修改 deployment，不符合 GitOps 最佳实践
- 现在使用 ConfigMap + overlay patch，通过 `update-images.sh` 统一管理
- 这些脚本的功能已经被更好的架构方案替代

### 2. 废弃目录

- ❌ `runner/` - 已废弃，现在使用 `images/runner/`

**原因**：
- 所有构建脚本已迁移到 `images/runner/`
- 旧的 `runner/Dockerfile` 不再使用

## 需要更新的文件

### 1. 脚本更新

- ⚠️ `scripts/rebuild-and-deploy-runner.sh` - 需要更新或删除
  - 当前使用 `kubectl set env` 直接修改 deployment
  - 应该改为使用 `update-images.sh` + `deploy.sh`

- ⚠️ `k8s/scripts/backup.sh` - 需要更新
  - 当前尝试从 env value 读取 runner 镜像
  - 应该改为从 ConfigMap 读取

- ⚠️ `k8s/scripts/verify.sh` - 需要更新
  - 当前尝试从 env value 读取 runner 镜像
  - 应该改为从 ConfigMap 读取

## 应该保留的文件

### 1. 文档文件（有价值）

- ✅ `k8s/ARCHITECTURE_IMPROVEMENTS.md` - 架构改进文档
- ✅ `k8s/REFACTORING_NOTES.md` - 重构说明
- ✅ `k8s/REFACTORING_SUMMARY.md` - 重构总结
- ✅ `k8s/ARCHITECTURE_COMPARISON.md` - 架构对比

**原因**：这些文档记录了架构改进的过程和设计决策，对未来的维护有价值。

### 2. 测试和验证脚本

- ✅ `k8s/scripts/test-version-update.sh` - 版本一致性验证脚本
- ✅ `manager-service/scripts/test.sh` - 单元测试脚本

**原因**：这些是必要的验证和测试工具。

## 清理步骤

1. 删除临时修复脚本
2. 删除废弃的 `runner/` 目录
3. 更新 `backup.sh` 和 `verify.sh` 使用 ConfigMap
4. 更新或删除 `rebuild-and-deploy-runner.sh`
5. 验证所有脚本正常工作
