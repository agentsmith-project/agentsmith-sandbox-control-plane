# GC 镜像重新构建和部署成功

## 执行的操作

### 1. 重新构建 GC 镜像 ✅

```bash
cd images/gc
docker buildx build \
  --platform linux/amd64 \
  --tag harbor.pullot.com:28443/agentsmith/sandbox-gc:1.0.0 \
  --load \
  --build-arg HTTP_PROXY="http://192.168.0.220:8889" \
  --build-arg HTTPS_PROXY="http://192.168.0.220:8889" \
  --build-arg http_proxy="http://192.168.0.220:8889" \
  --build-arg https_proxy="http://192.168.0.220:8889" \
  --progress=plain .
```

**结果**：
- 镜像大小：165MB
- 镜像 ID：c18b06c3ac16
- 构建成功 ✅

### 2. 推送到 Harbor ✅

```bash
docker push harbor.pullot.com:28443/agentsmith/sandbox-gc:1.0.0
```

**结果**：
- Digest: sha256:8e7ba2ddef6371537081868fdfbf6045e466d934e4b650fa120f49a424ab0dd7
- 推送成功 ✅

### 3. 更新 CronJob 配置 ✅

将 `imagePullPolicy` 从 `IfNotPresent` 改为 `Always`，确保使用最新镜像：

```bash
kubectl patch cronjob sandbox-gc -n sandbox-system \
  -p '{"spec":{"jobTemplate":{"spec":{"template":{"spec":{"containers":[{"name":"gc","imagePullPolicy":"Always"}]}}}}}}'
```

### 4. 验证 GC 功能 ✅

**测试结果**：
- 手动触发的 GC Job 成功执行
- 过期的 pod `sbx-ba7816bf8f` 已被删除 ✅
- GC 脚本正常工作 ✅

## 修复的问题

1. ✅ **GC 脚本语法错误**：修复了 `|||` 语法错误，改为正确的 `|`
2. ✅ **GC CronJob 配置**：清理了重复的代理环境变量
3. ✅ **镜像拉取策略**：改为 `Always` 确保使用最新镜像

## 当前状态

- **GC 镜像版本**: 1.0.0
- **镜像仓库**: harbor.pullot.com:28443/agentsmith/sandbox-gc:1.0.0
- **镜像拉取策略**: Always
- **GC 调度**: 每 1 分钟执行一次
- **功能状态**: ✅ 正常工作

## 验证命令

```bash
# 检查 GC CronJob 状态
kubectl get cronjob sandbox-gc -n sandbox-system

# 查看最新的 GC Job 日志
kubectl logs -n sandbox-system -l job-name --tail=50 | grep -E "(delete|expired)"

# 检查 runner pods
kubectl get pods -n sandbox -l app=llm-sandbox

# 手动触发 GC Job 测试
kubectl create job --from=cronjob/sandbox-gc manual-gc-test-$(date +%s) -n sandbox-system
```

## 总结

✅ **GC 功能已完全修复并正常工作**

- 脚本语法错误已修复
- 镜像已重新构建并推送到 Harbor
- CronJob 配置已更新
- 功能验证通过，过期 pod 已被正确删除

GC 现在会每分钟检查一次，自动删除过期的 runner pods。
