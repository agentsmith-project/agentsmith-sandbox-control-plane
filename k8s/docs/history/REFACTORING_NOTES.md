# K8s 部署脚本重构说明

## 重构目标

1. **修复 Kustomize 镜像替换问题**
2. **统一脚本实现方式**
3. **创建可复用的工具库**
4. **改进错误处理和日志**
5. **遵循最佳实践**

## 主要改进

### 1. 修复 Kustomize Base 配置

**问题**：Base 中定义了完整的镜像路径（包含 registry），导致 overlay 无法正确覆盖。

**解决方案**：
- Base 中只定义镜像名称，不包含 registry
- Overlay 中定义完整的镜像路径（包含 registry 和 tag）

**修改文件**：
- `k8s/base/kustomization.yaml`

```yaml
# 修改前
images:
  - name: sandbox-manager
    newName: localhost:5001/sandbox-manager  # ❌ 包含 registry
    newTag: "1.0.0"

# 修改后
images:
  - name: sandbox-manager
    newName: sandbox-manager  # ✅ 只包含镜像名称
    newTag: "1.0.0"
```

### 2. 创建统一的工具库

**新增文件**：
- `k8s/scripts/lib/common.sh` - 通用工具函数
- `k8s/scripts/lib/kustomize-utils.sh` - Kustomize 相关工具

**功能**：
- 统一的日志输出（带颜色）
- 配置加载函数
- 版本读取函数
- Registry 路径构建函数
- K8s 连接检查
- Kustomize 工具检查

### 3. 重构 update-images.sh

**改进前**：
- 混合了多种方法（kustomize、Python、sed）
- 代码重复
- 错误处理不统一

**改进后**：
- 使用工具库统一实现
- 优先使用 kustomize，fallback 到 Python
- 统一的错误处理和日志输出
- 清晰的函数分离

### 4. 改进 deploy.sh

**改进**：
- 使用工具库函数
- 统一的日志输出
- 更好的错误处理

## 文件结构

```
k8s/
├── base/
│   └── kustomization.yaml          # ✅ 修复：只定义镜像名称
├── overlays/
│   ├── dev/
│   │   └── kustomization.yaml     # ✅ 定义完整镜像路径
│   ├── staging/
│   └── production/
└── scripts/
    ├── lib/                        # ✅ 新增：工具库
    │   ├── common.sh               # 通用工具函数
    │   └── kustomize-utils.sh     # Kustomize 工具函数
    ├── deploy.sh                   # ✅ 重构：使用工具库
    └── update-images.sh            # ✅ 重构：统一实现方式
```

## 使用方式

### 更新镜像版本

```bash
cd k8s
REGISTRY=harbor.pullot.com:28443 \
HARBOR_PROJECT=agentsmith \
./scripts/update-images.sh
```

### 部署到环境

```bash
cd k8s
./scripts/deploy.sh dev
```

## 最佳实践

1. **单一职责原则**：每个脚本和函数只做一件事
2. **DRY 原则**：公共功能提取到工具库
3. **错误处理**：统一的错误处理和日志输出
4. **可维护性**：清晰的代码结构和注释
5. **可扩展性**：易于添加新功能

## 后续改进建议

1. **添加单元测试**：为工具函数添加测试
2. **文档完善**：添加更详细的函数文档
3. **CI/CD 集成**：在 CI/CD 中验证脚本
4. **配置验证**：添加配置文件的验证逻辑
5. **回滚机制**：改进回滚脚本使用工具库
