# GC 和 TTL 问题分析

## 当前配置

### TTL 设置（按环境）

- **Base（默认）**: 900 秒（15 分钟）
- **Dev 环境**: 300 秒（5 分钟）
- **Staging 环境**: 1800 秒（30 分钟）
- **Production 环境**: 3600 秒（1 小时）

### GC CronJob 配置

- **调度频率**: 每 1 分钟执行一次 (`*/1 * * * *`)
- **时间偏差容忍**: 5 秒 (`NOW_SKEW_SECONDS=5`)

## 发现的问题

### 1. GC 脚本语法错误 ❌

**问题**：第 18 行有 `|||` 语法错误

```bash
# 错误的代码
echo "${pods_json}" | jq -c '...' \
||| while read -r item; do
```

**影响**：
- 导致 `while` 循环无法执行
- GC 脚本只输出 JSON，但不处理 pod
- Pod 不会被删除，即使已过期

**修复**：改为正确的管道语法

```bash
# 正确的代码
echo "${pods_json}" | jq -c '...' | while read -r item; do
```

### 2. TTL 配置验证

**当前 Dev 环境配置**：
- ConfigMap: `ttl-seconds-default: "300"` ✅
- Pod annotations: `sandbox/ttlSeconds: "300"` ✅
- Pod expiresAt: 已过期（超过 3 分钟）✅

**结论**：TTL 配置正确，但 GC 脚本无法执行删除操作。

## 修复步骤

1. ✅ 修复 GC 脚本语法错误
2. 重新构建 GC 镜像
3. 重新部署 GC CronJob
4. 验证 GC 正常工作

## 验证方法

```bash
# 1. 检查 GC 脚本语法
bash -n images/gc/gc.sh

# 2. 检查 GC Job 日志
kubectl logs -n sandbox-system -l job-name | grep -E "(delete|expired)"

# 3. 检查过期 pod 是否被删除
kubectl get pods -n sandbox -l app=llm-sandbox
```

## 预期行为

修复后，GC 应该：
1. 每分钟执行一次
2. 检查所有 `app=llm-sandbox` 的 pod
3. 读取 `sandbox/expiresAt` 或计算 `lastActiveAt + ttl`
4. 如果 `now + 5秒 >= expiresAt`，删除 pod
5. 输出 "delete {pod-name} (expired)" 日志
