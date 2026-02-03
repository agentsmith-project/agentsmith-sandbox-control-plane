# GC 问题修复总结

## 发现的问题

### 1. GC 脚本语法错误 ✅ 已修复

**问题**：第 17-18 行使用了错误的语法 `|||`（三个管道符）

**老代码格式**（正确）：
```bash
echo "${pods_json}" | jq -c '...' \
| while read -r item; do
```

**新代码格式**（修复前，错误）：
```bash
echo "${pods_json}" | jq -c '...' \
||| while read -r item; do  # ❌ 语法错误
```

**修复后**：
```bash
echo "${pods_json}" | jq -c '...' | while read -r item; do  # ✅ 正确
```

### 2. GC CronJob 配置问题 ✅ 已修复

**问题**：重复的代理环境变量（HTTP_PROXY, HTTPS_PROXY 等出现了两次）

**修复**：已移除重复的环境变量定义

### 3. RBAC 权限 ✅ 已验证

- Role: `sandbox-gc-role` 有正确的权限（get, list, watch, delete pods）
- RoleBinding: 正确绑定 ServiceAccount
- 权限验证: `kubectl auth can-i delete pods` 返回 `yes`

## 修复内容

1. ✅ 修复 GC 脚本语法错误（`images/gc/gc.sh`）
2. ✅ 清理 GC CronJob 中重复的环境变量（`k8s/base/gc-cronjob.yaml`）

## 下一步操作

### 1. 重新构建 GC 镜像

```bash
cd images/gc
docker build -t sandbox-gc:1.0.0 .
```

### 2. 推送到 Registry（如果使用 Harbor）

```bash
docker tag sandbox-gc:1.0.0 harbor.pullot.com:28443/agentsmith/sandbox-gc:1.0.0
docker push harbor.pullot.com:28443/agentsmith/sandbox-gc:1.0.0
```

### 3. 更新镜像版本（如果需要）

```bash
cd k8s
REGISTRY=harbor.pullot.com:28443 HARBOR_PROJECT=agentsmith ./scripts/update-images.sh
```

### 4. 重新部署（或等待下次调度）

```bash
# 方式 1: 等待下次 CronJob 调度（每 1 分钟）
# 方式 2: 手动触发测试
kubectl create job --from=cronjob/sandbox-gc manual-gc-test -n sandbox-system
kubectl logs -n sandbox-system job/manual-gc-test
```

## 验证方法

```bash
# 1. 检查 GC 脚本语法
bash -n images/gc/gc.sh

# 2. 检查最新的 GC Job 日志
kubectl logs -n sandbox-system -l job-name --tail=50 | grep -E "(delete|expired)"

# 3. 检查过期 pod 是否被删除
kubectl get pods -n sandbox -l app=llm-sandbox

# 4. 检查 pod 的过期时间
kubectl get pods -n sandbox -l app=llm-sandbox -o jsonpath='{range .items[*]}{.metadata.name}{"\t"}{.metadata.annotations.sandbox/expiresAt}{"\n"}{end}'
```

## 预期行为

修复后，GC 应该：
1. ✅ 每分钟执行一次
2. ✅ 检查所有 `app=llm-sandbox` 的 pod
3. ✅ 读取 `sandbox/expiresAt` 或计算 `lastActiveAt + ttl`
4. ✅ 如果 `now + 5秒 >= expiresAt`，删除 pod
5. ✅ 输出 "delete {pod-name} (expired)" 日志

## 当前状态

- **TTL 配置**: Dev 环境 300 秒（5 分钟）✅
- **GC 脚本**: 语法已修复 ✅
- **RBAC 权限**: 配置正确 ✅
- **GC CronJob**: 配置已清理 ✅
- **需要操作**: 重新构建并部署 GC 镜像 ⚠️
