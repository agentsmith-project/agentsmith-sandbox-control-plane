# GC 问题分析报告

## 问题发现

### 1. GC 脚本格式差异

**老代码** (`sandbox-old/gc/gc.sh`):
```bash
echo "${pods_json}" | jq -c '...' \
| while read -r item; do
```

**新代码** (`sandbox/images/gc/gc.sh` - 修复前):
```bash
echo "${pods_json}" | jq -c '...' \
||| while read -r item; do  # ❌ 三个管道符，语法错误
```

**修复后**:
```bash
echo "${pods_json}" | jq -c '...' | while read -r item; do  # ✅ 正确
```

### 2. RBAC 权限检查

✅ **权限配置正确**：
- Role: `sandbox-gc-role` 有 `get`, `list`, `watch`, `delete` pods 权限
- RoleBinding: 正确绑定 `sandbox-system:sandbox-gc` ServiceAccount
- 权限验证: `kubectl auth can-i delete pods` 返回 `yes`

### 3. GC CronJob 配置

**发现的问题**：
- 新代码中有**重复的代理环境变量**（HTTP_PROXY, HTTPS_PROXY 等出现了两次）
- 这不会影响功能，但应该清理

## 根本原因

1. **语法错误**：`|||` 导致 `while` 循环无法执行
2. **脚本行为**：脚本只输出 JSON，但不处理 pod
3. **结果**：Pod 不会被删除，即使已过期

## 修复状态

✅ **已修复**：
- GC 脚本语法错误已修复
- 脚本现在使用正确的管道语法

⚠️ **需要操作**：
1. 重新构建 GC 镜像
2. 重新部署 GC CronJob（或等待下次调度）
3. 验证 GC 正常工作

## 验证步骤

```bash
# 1. 检查 GC 脚本语法
bash -n images/gc/gc.sh

# 2. 检查最新的 GC Job 日志
kubectl logs -n sandbox-system -l job-name --tail=50 | grep -E "(delete|expired)"

# 3. 检查过期 pod 是否被删除
kubectl get pods -n sandbox -l app=llm-sandbox

# 4. 手动触发 GC Job 测试
kubectl create job --from=cronjob/sandbox-gc manual-gc-test -n sandbox-system
kubectl logs -n sandbox-system job/manual-gc-test
```

## 对比总结

| 项目 | 老代码 | 新代码（修复前） | 新代码（修复后） |
|------|--------|------------------|------------------|
| 脚本格式 | `\` + `\|` | `\` + `\|\|\|` ❌ | `\|` ✅ |
| RBAC 权限 | ✅ 正确 | ✅ 正确 | ✅ 正确 |
| 功能 | ✅ 正常工作 | ❌ 不工作 | ✅ 应该正常工作 |

## 建议

1. **立即修复**：重新构建并部署 GC 镜像
2. **清理配置**：移除 GC CronJob 中重复的代理环境变量
3. **监控验证**：观察下次 GC 执行是否正常删除过期 pod
