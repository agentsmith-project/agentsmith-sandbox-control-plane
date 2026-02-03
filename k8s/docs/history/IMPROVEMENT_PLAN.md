# 代码库改进计划

## 目标
将代码库质量从 **8.5/10** 提升到 **9.5/10**，完全符合最佳实践。

## 改进原则
1. **删除废弃代码**：坚决删除不再使用的文件和逻辑
2. **重构优化**：不怕重构，追求最佳实践
3. **配置化**：减少硬编码，提高可配置性
4. **简化**：提取公共逻辑，减少重复代码
5. **测试**：添加测试提高可靠性

---

## Phase 1: 清理废弃代码（高优先级）

### 1.1 删除废弃的 `scripts/deploy.sh`

**问题**：
- 使用 `sed -i` 直接修改 YAML 文件
- 引用不存在的 `manifests/` 目录
- 已被 `k8s/scripts/deploy.sh`（基于 Kustomize）替代

**操作**：
```bash
# 1. 确认不再使用
grep -r "scripts/deploy.sh" --exclude-dir=.git

# 2. 更新所有引用到 k8s/scripts/deploy.sh
# 3. 删除文件
rm scripts/deploy.sh
```

**影响文件**：
- `scripts/deploy-all.sh` - 已使用 `k8s/scripts/deploy.sh` ✅
- `scripts/deploy-with-harbor.sh` - 需要检查
- `scripts/rebuild-and-deploy-runner.sh` - 已使用 `k8s/scripts/deploy.sh` ✅
- 文档中的引用需要更新

### 1.2 检查并清理其他可能废弃的脚本

**检查项**：
- `scripts/verify-runner-image.sh` - 检查是否还在使用
- `scripts/verify-all.sh` - 检查是否还在使用
- 其他可能重复功能的脚本

---

## Phase 2: 重构脚本结构（高优先级）

### 2.1 简化 `rebuild-and-deploy-runner.sh`

**问题**：
- 脚本过长（158行）
- 包含大量重复逻辑
- 可以提取公共函数

**改进方案**：
1. 提取构建逻辑到 `lib/build-utils.sh`
2. 提取部署逻辑到 `lib/deploy-utils.sh`
3. 简化主脚本，只调用函数

**新结构**：
```bash
# lib/build-utils.sh
build_runner_image() {
    # 构建逻辑
}

# lib/deploy-utils.sh
deploy_runner_image() {
    # 部署逻辑
}

# scripts/rebuild-and-deploy-runner.sh
source lib/build-utils.sh
source lib/deploy-utils.sh

build_runner_image ...
deploy_runner_image ...
```

### 2.2 统一构建脚本

**问题**：
- `build-images-buildx.sh` 中有重复的构建逻辑
- 可以提取公共函数

**改进方案**：
- 创建 `lib/build-utils.sh` 包含：
  - `setup_buildx_builder()`
  - `build_image_with_buildx()`
  - `load_image_to_kind()`

---

## Phase 3: 配置化硬编码值（中优先级）

### 3.1 GC CronJob 配置化

**问题**：
- `NOW_SKEW_SECONDS=5` 硬编码
- `schedule="*/1 * * * *"` 硬编码

**改进方案**：
1. 移到 ConfigMap
2. 通过环境变量读取

**新结构**：
```yaml
# base/configmap.yaml
data:
  gc-schedule: "*/1 * * * *"
  gc-skew-seconds: "5"

# base/gc-cronjob.yaml
env:
- name: NOW_SKEW_SECONDS
  valueFrom:
    configMapKeyRef:
      name: sandbox-config
      key: gc-skew-seconds
```

### 3.2 Registry 默认值配置化

**问题**：
- `localhost:5001` 硬编码在多个地方

**改进方案**：
- 统一从环境变量或配置文件读取
- 创建 `k8s/config/registry.env` 管理默认值

---

## Phase 4: 文档整理（中优先级）

### 4.1 创建文档索引

**问题**：
- `k8s/` 目录下有多个文档文件
- 缺少统一的文档索引

**改进方案**：
1. 创建 `k8s/docs/INDEX.md` 作为文档索引
2. 将过程性文档移到 `k8s/docs/history/`
3. 保留核心文档在 `k8s/` 根目录

**新结构**：
```
k8s/
├── README.md                    # 主要文档
├── docs/
│   ├── INDEX.md                 # 文档索引
│   ├── ARCHITECTURE.md          # 架构文档（合并多个）
│   ├── DEPLOYMENT.md            # 部署文档
│   └── history/                 # 过程性文档
│       ├── REFACTORING_NOTES.md
│       ├── REFACTORING_SUMMARY.md
│       └── ...
```

### 4.2 更新文档引用

**操作**：
- 更新所有对废弃脚本的引用
- 更新文档中的过时信息

---

## Phase 5: 错误处理改进（中优先级）

### 5.1 统一错误处理模式

**问题**：
- 部分脚本错误处理不一致
- 缺少详细的错误信息

**改进方案**：
1. 在 `lib/common.sh` 中添加：
   - `handle_error()` - 统一错误处理
   - `validate_required()` - 参数验证
   - `check_prerequisites()` - 前置条件检查

2. 所有脚本使用统一的错误处理

---

## Phase 6: 测试增强（低优先级）

### 6.1 添加脚本测试

**改进方案**：
1. 创建 `k8s/scripts/tests/` 目录
2. 为关键脚本添加测试：
   - `test-update-images.sh`
   - `test-deploy.sh`
   - `test-version-consistency.sh`

### 6.2 添加集成测试

**改进方案**：
- 创建 `tests/integration/` 目录
- 添加端到端测试

---

## 执行计划

### 阶段 1: 清理（立即执行）
1. ✅ 确认废弃文件
2. ✅ 删除 `scripts/deploy.sh`
3. ✅ 更新所有引用
4. ✅ 检查其他废弃脚本

### 阶段 2: 重构（1-2天）
1. ✅ 创建 `lib/build-utils.sh`
2. ✅ 创建 `lib/deploy-utils.sh`
3. ✅ 重构 `rebuild-and-deploy-runner.sh`
4. ✅ 重构 `build-images-buildx.sh`

### 阶段 3: 配置化（1天）
1. ✅ GC 配置移到 ConfigMap
2. ✅ Registry 默认值配置化
3. ✅ 更新所有相关文件

### 阶段 4: 文档整理（0.5天）
1. ✅ 创建文档索引
2. ✅ 整理文档结构
3. ✅ 更新文档引用

### 阶段 5: 错误处理（0.5天）
1. ✅ 统一错误处理函数
2. ✅ 更新所有脚本

### 阶段 6: 测试（可选，1-2天）
1. ✅ 添加脚本测试
2. ✅ 添加集成测试

---

## 预期效果

### 改进前（8.5/10）
- ⚠️ 有废弃代码
- ⚠️ 脚本结构可以优化
- ⚠️ 硬编码值较多
- ⚠️ 文档分散

### 改进后（9.5/10）
- ✅ 无废弃代码
- ✅ 脚本结构清晰，工具库完善
- ✅ 配置化程度高
- ✅ 文档组织良好
- ✅ 错误处理统一
- ✅ 测试覆盖完善

---

## 风险评估

### 低风险
- 删除废弃文件（已确认不使用）
- 文档整理（不影响功能）

### 中风险
- 脚本重构（需要充分测试）
- 配置化（需要验证所有环境）

### 缓解措施
- 每个阶段完成后进行验证
- 保留 Git 历史，可以回滚
- 充分测试后再合并
