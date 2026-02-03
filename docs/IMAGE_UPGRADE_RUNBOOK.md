# 镜像升级操作手册（从打包到部署）

适用范围：
- 仅 `linux/amd64`
- 镜像：`sandbox-manager` / `sandbox-runner` / `sandbox-gc`
- Registry：Harbor（仅用户名/密码）
- 代理策略：
  - 宿主机拉取依赖可用代理（`HTTP_PROXY/HTTPS_PROXY`）
  - 访问 Harbor 必须不走代理（`NO_PROXY` 包含 Harbor 主机名；环境里若有 `ALL_PROXY` 也要确保对 Harbor 不生效）
  - `docker build` 的 Dockerfile 内部命令不使用代理（`--build-proxy off`；且镜像内不残留代理环境变量）

本文给低经验运维的“一步步照做”流程：分 **在线升级** 和 **离线升级** 两条路径。  
强烈建议所有命令都在仓库根目录执行，且使用同一个终端会话。

---

## 0. 准备（两条路径都要做）

1) 进入仓库根目录：
```bash
cd /path/to/sandbox
```

2) 准备环境变量文件（推荐，避免手敲出错）：
- 将以下变量写入 `secrets/test.env`（该目录默认不提交 git）：
  - `HARBOR_REGISTRY=harbor.xxx.com:28443`
  - `HARBOR_PROJECT=agentsmith`
  - `HARBOR_USERNAME=admin`
  - `HARBOR_PASSWORD='******'`
  - `HTTP_PROXY=http://192.168.0.220:8889`（如需要）
  - `HTTPS_PROXY=http://192.168.0.220:8889`（如需要）
  - `NO_PROXY=localhost,127.0.0.1,harbor.xxx.com`（必须包含 Harbor 主机名）

3) 加载环境变量：
```bash
set -a
source secrets/test.env
set +a
```

4) 确认 Harbor 的 CA 证书文件存在（kind 部署需要）：
- 优先使用：`secrets/harbor-ca.crt`
- 没有的话再走脚本自动拉取（脚本会 fallback 到 `openssl s_client`）

5) 拉取/校验随包工具（推荐，不依赖宿主机安装 kubectl/kustomize/skopeo/jq/yq）：
```bash
./sbx tools fetch --proxy auto
./sbx tools verify
```

---

## 1. 必读：更新 runner 时特别要注意什么（强制检查清单）

更新 `sandbox-runner` 时，最容易踩坑的点：

1) **Manager 配置里的 runner 镜像必须是“完整引用”**
- 例如：`harbor.xxx.com:28443/agentsmith/sandbox-runner:1.0.0`
- 不能是 `sandbox-runner:1.0.0`、也不能是 `localhost:5001/...`、更不能是 `docker.io/library/...`

2) **拉取凭证必须同时存在于两个 namespace**
- `sandbox-system`：manager 部署所在
- `sandbox`：运行 session pod 的 namespace
- 所以每次升级后都建议执行一次：
```bash
./sbx k8s harbor-secret \
  --registry "$HARBOR_REGISTRY" --project "$HARBOR_PROJECT" \
  --username "$HARBOR_USERNAME" --password "$HARBOR_PASSWORD"
```

3) **代理必须“分责任域”**
- 访问 Harbor：必须绕过代理（`--proxy off` + `NO_PROXY`）
- build 阶段：必须 `--build-proxy off`，避免代理写入最终镜像

4) **验证一定要包含一次“实际创建 sandbox pod”**
- 仅 `deploy/verify` 不够，runner 的问题通常在 session pod 拉取阶段才暴露。

---

## 2. 在线升级（推荐：直接推 Harbor → 部署）

适用场景：目标集群能访问 Harbor，且你可以直接推镜像到 Harbor。

### 2.1 设置版本号（按需）

根据你改动的组件，更新对应版本文件（示例）：
- runner：`images/runner/VERSION`
- gc：`images/gc/VERSION`
- manager：`manager-service/VERSION`

版本号变更后，继续下面步骤。

### 2.2 构建镜像（linux/amd64，build 不用代理）

只更新 runner（推荐先最小化范围）：
```bash
./sbx images build --pull-proxy on --build-proxy off --platform linux/amd64 --only runner
```

同时更新多个镜像：
```bash
./sbx images build --pull-proxy on --build-proxy off --platform linux/amd64
```

### 2.3 推送到 Harbor（绕过代理，source=archive）

只推 runner：
```bash
./sbx images push harbor \
  --registry "$HARBOR_REGISTRY" --project "$HARBOR_PROJECT" \
  --username "$HARBOR_USERNAME" --password "$HARBOR_PASSWORD" \
  --proxy off --source archive --build-proxy off --only runner
```

### 2.4 更新 k8s 清单引用（把镜像指向 Harbor）

```bash
./sbx k8s update-images --registry "$HARBOR_REGISTRY" --project "$HARBOR_PROJECT"
```

### 2.5 更新/创建 Harbor 拉取 secret（两个 namespace）

```bash
./sbx k8s harbor-secret \
  --registry "$HARBOR_REGISTRY" --project "$HARBOR_PROJECT" \
  --username "$HARBOR_USERNAME" --password "$HARBOR_PASSWORD"
```

### 2.6 部署并验证（按环境选择）

生产环境：
```bash
./sbx k8s deploy production
./sbx k8s verify production
```

### 2.7 Runner 升级后强制验证（创建一次 sandbox）

1) 取 service key（用 vendored kubectl）：
```bash
K=tools/bin/linux-amd64/kubectl
SERVICE_KEY=$($K -n sandbox-system get secret sandbox-manager-keys -o jsonpath='{.data.SERVICE_KEYS}' | base64 -d)
SERVICE_KEY=${SERVICE_KEY%%,*}
```

2) Port-forward manager：
```bash
$K -n sandbox-system port-forward svc/sandbox-manager 18080:80
```

3) 运行 e2e（会创建/删除 sandbox，能触发 runner 拉取）：
```bash
cd manager-service
./scripts/test-manager.sh http://127.0.0.1:18080 "$SERVICE_KEY"
```

4) 看到 `PASS` 后，返回根目录并确认 `sandbox` namespace 没有残留 pod：
```bash
cd ..
$K -n sandbox get pods
```

---

## 3. 离线升级（在线机打包 → 现场导入 → 部署）

适用场景：客户现场 air-gapped，只能通过离线包交付。

### 3.1 在线机：构建（同 2.2）

```bash
./sbx tools fetch --proxy on
./sbx tools verify
./sbx images build --pull-proxy on --build-proxy off --platform linux/amd64
```

### 3.2 在线机：导出离线包并校验

```bash
OUT="dist/sandbox-offline-$(date +%Y%m%d-%H%M%S)"
./sbx images export offline --out "$OUT"
./sbx images verify offline --path "$OUT"
```

把整个 `$OUT` 目录拷贝到客户现场（U 盘/移动盘等）。

### 3.3 现场：校验离线包（必须）

```bash
./sbx images verify offline --path /path/to/sandbox-offline-*
```

### 3.4 现场：导入到 Harbor（推荐：直接从 docker-archive tar 推送）

```bash
./sbx images import offline \
  --from /path/to/sandbox-offline-* \
  --to harbor \
  --registry "$HARBOR_REGISTRY" --project "$HARBOR_PROJECT" \
  --username "$HARBOR_USERNAME" --password "$HARBOR_PASSWORD" \
  --verify --proxy off
```

### 3.5 现场：部署（同 2.4~2.7）

```bash
./sbx k8s harbor-secret \
  --registry "$HARBOR_REGISTRY" --project "$HARBOR_PROJECT" \
  --username "$HARBOR_USERNAME" --password "$HARBOR_PASSWORD"

./sbx k8s update-images --registry "$HARBOR_REGISTRY" --project "$HARBOR_PROJECT"

./sbx k8s deploy production
./sbx k8s verify production
```

随后按 **2.7** 执行 runner 强制验证。

---

## 4. 回滚（建议先问负责人再做）

回滚原则：回滚的是“镜像 tag + k8s 引用”，不是只回滚 deployment。

1) 确认 Harbor 上可用的旧 tag（在 Harbor UI 或 API 查看）。
2) 把 `k8s` 指向旧 tag（建议用 `./sbx k8s update-images` 指向旧 tag 后再 deploy；若工具暂不支持指定旧 tag，则手工调整 overlay 中镜像引用并提交变更）。
3) `./sbx k8s deploy production` + `./sbx k8s verify production`
4) 再按 **2.7** 触发一次 sandbox 创建验证 runner。

